package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/outerloop"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This file is the early ticket-close pullback (RUYI-660, M1): an agent
// running under an active plan must not end the loop by closing the ticket —
// the loop ends on the STAGE_COMPLETE end marker in its report. A closure
// without the marker is compensated after the commit: the status flips back
// to what it was, and a system comment tells the agent why.
//
// The hook is the second half of an issue-status write, so it lives beside
// UpdateIssue with that layer's before/after rows and event helpers. It is
// best-effort end to end: a failed pullback is logged and announced in a
// system comment, and the stall sweep (OuterLoopSweepOnce) closes the
// consistency gap on its next pass.
//
// Everything here is gated on the OuterLoop flag AND on the issue carrying
// an active running plan in metadata.

// onOuterLoopIssueStatusChanged judges a committed status transition on a
// plan-carrying issue. prev is the pre-write row, next the post-write row.
func (h *Handler) onOuterLoopIssueStatusChanged(ctx context.Context, prev, next db.Issue, actorType, actorID string) {
	if !featureflags.OuterLoopEnabled(ctx, h.FeatureFlags) {
		return
	}
	plan, ok := outerloop.ParsePlan(util.JSONObjectOrEmpty(next.Metadata))
	if !ok || plan.State != outerloop.StateRunning {
		return
	}
	switch issuestatus.Effective(ctx, h.Queries, next.WorkspaceID, next.Status) {
	case "done", "cancelled":
	default:
		return // not a closure
	}

	if actorType == "member" {
		// A member closing the ticket is an intentional stop: retire the
		// plan, never revert their hand.
		h.outerLoopStopPlanSilently(ctx, next)
		return
	}

	// Agent closure. The contract: closing requires a stage-end report
	// carrying the marker — the agent's latest comment is the evidence.
	agentID, err := util.ParseUUID(actorID)
	if err != nil {
		return // non-agent actor shape cannot hold a plan; nothing to judge
	}
	report, err := h.Queries.GetLastCommentByAuthorOnIssue(ctx, db.GetLastCommentByAuthorOnIssueParams{
		IssueID:     next.ID,
		WorkspaceID: next.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    agentID,
	})
	if err == nil && outerloop.HasEndMarker(report.Content) {
		// Legitimate stage-end close: retire the plan, keep the closure.
		h.outerLoopStopPlanSilently(ctx, next)
		return
	}
	h.outerLoopPullbackClosedTicket(ctx, prev, next, plan)
}

// outerLoopPullbackClosedTicket reverts an agent's marker-less closure. The
// revert is guarded on the row still sitting in the closed state that was
// observed: a member editing the ticket in between wins, and the revert
// no-ops (pgx.ErrNoRows path — logged, announced, sweep re-judges).
func (h *Handler) outerLoopPullbackClosedTicket(ctx context.Context, prev, next db.Issue, plan outerloop.Plan) {
	reverted, err := h.Queries.UpdateIssueStatusOnly(ctx, db.UpdateIssueStatusOnlyParams{
		Status:                prev.Status,
		ID:                    next.ID,
		WorkspaceID:           next.WorkspaceID,
		ExpectedCurrentStatus: next.Status,
	})
	if err != nil {
		// Leader's fallback note: the closure stands, the loop keeps its
		// running state, and the sweep continues the plan by rounds.
		slog.Error("outer loop: ticket-close pullback failed",
			"issue_id", uuidToString(next.ID),
			"closed_status", next.Status,
			"wanted_status", prev.Status,
			"error", err)
		h.outerLoopSystemComment(ctx, next, fmt.Sprintf(
			"外循环拉回失败：Issue 在缺少 STAGE_COMPLETE 结束回报的情况下被关闭为 %s，自动恢复未成功。外循环计划保持 running，稍后由停摆巡检按轮次继续推进。当前第 %d/%d 轮。",
			next.Status, plan.Round, plan.MaxRounds))
		return
	}

	prefix := h.getIssuePrefix(ctx, reverted.WorkspaceID)
	resp := issueToResponse(reverted, prefix)
	h.fillStatusCategory(ctx, reverted.WorkspaceID, &resp)
	h.publish(protocol.EventIssueUpdated, uuidToString(reverted.WorkspaceID), "system", "", map[string]any{
		"issue":          resp,
		"status_changed": true,
	})
	h.outerLoopSystemComment(ctx, next, fmt.Sprintf(
		"外循环拉回：Issue 在缺少 STAGE_COMPLETE 结束回报的情况下被关闭，已恢复为 %s。请先发布以 STAGE_COMPLETE 结尾的阶段结束回报，再把 Issue 置 in_review；如需中止计划，请由成员将 outerloop_state 置为 stopped。当前第 %d/%d 轮。",
		prev.Status, plan.Round, plan.MaxRounds))
}

// outerLoopStopPlanSilently retires the plan (state=stopped) through the
// single-key atomic write and mirrors the metadata event so live clients see
// the transition.
func (h *Handler) outerLoopStopPlanSilently(ctx context.Context, issue db.Issue) {
	updated, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Key:         outerloop.MetaState,
		Value:       []byte(`"` + outerloop.StateStopped + `"`),
	})
	if err != nil {
		slog.Error("outer loop: plan stop write failed",
			"issue_id", uuidToString(issue.ID), "error", err)
		return
	}
	h.publish(protocol.EventIssueMetadataChanged, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"issue_id":       uuidToString(updated.ID),
		"metadata":       util.JSONObjectOrEmpty(updated.Metadata),
		"issue_revision": updated.Revision,
	})
}

// outerLoopSystemComment posts a system-authored comment. Comment listeners
// short-circuit on author_type='system', so these announcements never wake
// an agent — they are purely for the humans reading the thread.
func (h *Handler) outerLoopSystemComment(ctx context.Context, issue db.Issue, content string) {
	created, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		ID:          dbid.NewV7(),
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     content,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("outer loop: system comment failed",
			"issue_id", uuidToString(issue.ID), "error", err)
		return
	}
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"comment":     commentToResponse(created.Comment(), nil, nil),
		"issue_title": issue.Title,
	})
}
