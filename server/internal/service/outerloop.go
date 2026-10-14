package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/outerloop"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This file wires the explicit continuation outer loop (RUYI-660, M2) into
// the server: task-terminal evaluation (auto next round / soft stop /
// wait-for-human), member-reply intent routing while waiting, and the stall
// sweep that self-heals the paths a synchronous hook cannot see. The early
// ticket-close pullback (M1) lives in the handler — it is the compensating
// second half of an issue-status write and needs that layer's before/after
// rows and event helpers.
//
// Every entry point is gated on the OuterLoop feature flag AND on the issue
// carrying an active plan in metadata: plan-less issues behave exactly as
// before this file existed.
//
// Plan state changes go through single-key atomic SQL (SetIssueMetadataKey /
// IncrementOuterLoopRound / SetOuterLoopResumedState) — never an
// application-layer read-modify-write of the metadata blob, which would race
// concurrent handler writes into lost updates.

// outerLoopTaskTerminalCompleted / outerLoopTaskTerminalFailed name the two
// outcomes the daemon completion and failure handlers report.
const (
	outerLoopTaskTerminalCompleted = "completed"
	outerLoopTaskTerminalFailed    = "failed"
)

// OuterLoopSweepResult summarizes one stall-sweep pass for the scheduler
// audit row.
type OuterLoopSweepResult struct {
	Scanned     int `json:"scanned"`
	Resumed     int `json:"resumed"`
	SoftStopped int `json:"soft_stopped"`
	Stopped     int `json:"stopped"`
}

// outerLoopSweepGrace is how long after a task terminalized before the sweep
// may act on the issue. The synchronous completion hook owns the first
// moments after a terminal transition; the sweep exists for what that hook
// could not see (server-side sweeper terminalizations, a lost enqueue race,
// a comment that landed after the hook evaluated). One cadence plus margin.
const outerLoopSweepGrace = 10 * time.Minute

// OnOuterLoopTaskTerminal evaluates a plan-carrying issue right after one of
// its tasks reached a terminal state for the first time. outcome is
// "completed" or "failed"; failureReason is the canonical taskfailure value
// for failures (empty otherwise).
//
// Decision tree (RUYI-660 设计小节 4):
//
//	completed → member owns the turn?  → skip (mention/reconcile paths own it)
//	            → agent report marker?  → stop silently
//	            → next > max?           → soft stop (brief/max preserved)
//	            → otherwise             → enqueue round next with handoff doc
//	failed    → pipeline class?       → leave for the stall sweep
//	            → otherwise           → waiting_human + ask-a-human comment
func (s *TaskService) OnOuterLoopTaskTerminal(ctx context.Context, task db.AgentTaskQueue, outcome, failureReason string) {
	if !featureflags.OuterLoopEnabled(ctx, s.FeatureFlags) {
		return
	}
	if !task.IssueID.Valid {
		return
	}
	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		return // issue gone: nothing to loop
	}
	plan, ok := s.outerLoopPlan(ctx, issue)
	if !ok || plan.State != outerloop.StateRunning {
		return
	}
	planAgent := outerLoopPlanAgentID(issue, plan)
	if !planAgent.Valid || planAgent != task.AgentID {
		return
	}

	switch outcome {
	case outerLoopTaskTerminalCompleted:
		s.outerLoopOnCompleted(ctx, issue, plan, planAgent)
	case outerLoopTaskTerminalFailed:
		s.outerLoopOnFailed(ctx, issue, plan, failureReason)
	}
}

func (s *TaskService) outerLoopOnCompleted(ctx context.Context, issue db.Issue, plan outerloop.Plan, planAgent pgtype.UUID) {
	// 成员优先: a member comment newer than everything the agent wrote means
	// the conversation turn is the member's — the mention trigger or the
	// completion reconcile owns the next move, and auto-continuing over it
	// would talk over the human.
	if s.outerLoopLastCommentIsMembers(ctx, issue) {
		return
	}
	report, err := s.Queries.GetLastCommentByAuthorOnIssue(ctx, db.GetLastCommentByAuthorOnIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    planAgent,
	})
	if err != nil {
		// The agent has not posted yet (comment raced the terminal callback)
		// — the sweep re-judges after the grace window.
		return
	}
	if outerloop.HasEndMarker(report.Content) {
		s.outerLoopStopSilently(ctx, issue)
		return
	}
	next := plan.Round + 1
	if next > plan.MaxRounds {
		s.outerLoopSoftStop(ctx, issue, plan)
		return
	}
	s.outerLoopDispatchRound(ctx, issue, plan, next)
}

func (s *TaskService) outerLoopOnFailed(ctx context.Context, issue db.Issue, plan outerloop.Plan, failureReason string) {
	if outerloop.IsPipelineClass(failureReason) {
		// Transient infrastructure failure: no human needed. The stall sweep
		// pulls the loop back after its grace window, so a rate-limit storm
		// cannot burn rounds faster than the sweep cadence.
		return
	}
	reason := failureReason
	if reason == "" {
		reason = "unknown"
	}
	waitReason := fmt.Sprintf("任务失败（%s），外循环暂停等待人工处理", reason)
	s.outerLoopSetMetaKey(ctx, issue, outerloop.MetaState, `"`+outerloop.StateWaitingHuman+`"`)
	s.outerLoopSetMetaKey(ctx, issue, outerloop.MetaWaitReason, jsonString(waitReason))
	s.outerLoopSystemComment(ctx, issue, fmt.Sprintf(
		"外循环暂停：任务失败（%s）。人工处理后在等待态下回复即可继续（回复含「停止」则结束计划）；当前第 %d/%d 轮。",
		reason, plan.Round, plan.MaxRounds))
}

// OnOuterLoopMemberComment routes a member's comment on a waiting_human
// plan (RUYI-660 设计小节 5). The caller guarantees member authorship and
// that decision-card answer comments have already been consumed elsewhere.
func (s *TaskService) OnOuterLoopMemberComment(ctx context.Context, issue db.Issue, comment db.Comment) {
	if !featureflags.OuterLoopEnabled(ctx, s.FeatureFlags) {
		return
	}
	plan, ok := s.outerLoopPlan(ctx, issue)
	if !ok || plan.State != outerloop.StateWaitingHuman {
		return
	}
	planAgent := outerLoopPlanAgentID(issue, plan)
	if !planAgent.Valid {
		return
	}

	if outerloop.ClassifyReply(comment.Content) == outerloop.IntentStop {
		s.outerLoopSetMetaKey(ctx, issue, outerloop.MetaState, `"`+outerloop.StateStopped+`"`)
		s.outerLoopSystemComment(ctx, issue, "外循环已按成员指示停止。重新开启可将 outerloop_state 置回 running（轮次清零、目标与上限保留）。")
		return
	}

	// Continue intent. The member's comment has already been through the
	// platform's own trigger pass (member comments wake the assignee; an
	// @mention dispatches explicitly), so a pending task for this pair —
	// including the one this very comment created — is the loop's resume
	// ride: only the waiting state needs clearing. Enqueueing a second task
	// on top would double-run against the one-pending-task unique index.
	pending, err := s.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
		IssueID: issue.ID,
		AgentID: planAgent,
		HeadSha: pgtype.Text{},
	})
	if err != nil {
		slog.Error("outer loop: pending-task dedup check failed",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	if pending {
		if _, err := s.Queries.SetOuterLoopResumedState(ctx, db.SetOuterLoopResumedStateParams{
			ID: issue.ID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Error("outer loop: resume state write failed",
				"issue_id", util.UUIDToString(issue.ID), "error", err)
		}
		return
	}
	// Dispatch with the member's comment as the trigger: the run's opening
	// context carries it, semantically identical to a mention dispatch.
	if _, err := s.EnqueueTaskForIssue(ctx, issue, comment.ID); err != nil {
		// Leave the plan waiting: the member's next comment (or the sweep
		// once a task exists) retries; failing loudly here would strand a
		// resumed plan with no task behind it.
		slog.Error("outer loop: resume enqueue failed",
			"issue_id", util.UUIDToString(issue.ID),
			"comment_id", util.UUIDToString(comment.ID), "error", err)
		return
	}
	if _, err := s.Queries.SetOuterLoopResumedState(ctx, db.SetOuterLoopResumedStateParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Error("outer loop: resume state write after enqueue failed",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
	}
}

// OuterLoopSweepOnce scans every running plan and pulls stalled loops back:
// no active task, the agent owning the latest comment, and the last
// terminal task older than the grace window. It re-runs the same verdict as
// the terminal hook, which makes it the consistency net for server-side
// sweeper terminalizations, lost enqueue races, and the closed-ticket
// fallback where M1's revert could not land (RUYI-660 设计小节 8).
func (s *TaskService) OuterLoopSweepOnce(ctx context.Context) (OuterLoopSweepResult, error) {
	var res OuterLoopSweepResult
	if !featureflags.OuterLoopEnabled(ctx, s.FeatureFlags) {
		return res, nil
	}
	issues, err := s.Queries.ListOuterLoopRunningIssues(ctx)
	if err != nil {
		return res, fmt.Errorf("outer loop sweep: list running plans: %w", err)
	}
	for _, issue := range issues {
		res.Scanned++
		plan, ok := s.outerLoopPlan(ctx, issue)
		if !ok || plan.State != outerloop.StateRunning {
			continue
		}
		planAgent := outerLoopPlanAgentID(issue, plan)
		if !planAgent.Valid {
			continue
		}
		active, err := s.Queries.HasActiveTaskForIssueAndAgent(ctx, db.HasActiveTaskForIssueAndAgentParams{
			IssueID: issue.ID, AgentID: planAgent,
		})
		if err != nil {
			slog.Error("outer loop sweep: active-task check failed",
				"issue_id", util.UUIDToString(issue.ID), "error", err)
			continue
		}
		if active {
			continue
		}
		if s.outerLoopLastCommentIsMembers(ctx, issue) {
			continue
		}
		if lastTerminal, err := s.Queries.GetLastTerminalTaskCompletedAtForIssueAndAgent(ctx, db.GetLastTerminalTaskCompletedAtForIssueAndAgentParams{
			IssueID: issue.ID, AgentID: planAgent,
		}); err == nil && time.Since(lastTerminal.Time) < outerLoopSweepGrace {
			continue
		}
		report, err := s.Queries.GetLastCommentByAuthorOnIssue(ctx, db.GetLastCommentByAuthorOnIssueParams{
			IssueID:     issue.ID,
			WorkspaceID: issue.WorkspaceID,
			AuthorType:  "agent",
			AuthorID:    planAgent,
		})
		if err != nil {
			continue // nothing to judge yet
		}
		if outerloop.HasEndMarker(report.Content) {
			s.outerLoopStopSilently(ctx, issue)
			res.Stopped++
			continue
		}
		next := plan.Round + 1
		if next > plan.MaxRounds {
			s.outerLoopSoftStop(ctx, issue, plan)
			res.SoftStopped++
			continue
		}
		s.outerLoopDispatchRound(ctx, issue, plan, next)
		res.Resumed++
	}
	return res, nil
}

// outerLoopDispatchRound enqueues continuation round next with the round
// handoff document. The round counter advances only after the enqueue
// lands, so a failed enqueue leaves the stored round pointing at the last
// round actually dispatched — the sweep's next pass retries the same number.
func (s *TaskService) outerLoopDispatchRound(ctx context.Context, issue db.Issue, plan outerloop.Plan, next int) {
	pending, err := s.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
		IssueID: issue.ID,
		AgentID: outerLoopPlanAgentID(issue, plan),
		HeadSha: pgtype.Text{},
	})
	if err != nil {
		slog.Error("outer loop: dispatch dedup check failed",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	if pending {
		return // a newer dispatch already owns the loop
	}
	handoff := outerloop.RoundDoc(next, plan.MaxRounds, plan.Goal)
	if _, err := s.EnqueueTaskForIssueWithHandoff(ctx, issue, handoff, pgtype.UUID{}); err != nil {
		slog.Error("outer loop: continuation enqueue failed",
			"issue_id", util.UUIDToString(issue.ID),
			"round", next, "max", plan.MaxRounds, "error", err)
		return
	}
	if _, err := s.Queries.IncrementOuterLoopRound(ctx, db.IncrementOuterLoopRoundParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Error("outer loop: round increment failed after enqueue",
			"issue_id", util.UUIDToString(issue.ID), "round", next, "error", err)
	}
}

func (s *TaskService) outerLoopStopSilently(ctx context.Context, issue db.Issue) {
	s.outerLoopSetMetaKey(ctx, issue, outerloop.MetaState, `"`+outerloop.StateStopped+`"`)
}

func (s *TaskService) outerLoopSoftStop(ctx context.Context, issue db.Issue, plan outerloop.Plan) {
	s.outerLoopStopSilently(ctx, issue)
	s.outerLoopSystemComment(ctx, issue, fmt.Sprintf(
		"外循环已达到最大轮次（%d/%d），计划软停机。任务目标与轮次上限已保留，将 outerloop_state 置回 running 可复启（轮次清零）。",
		plan.Round, plan.MaxRounds))
}

// outerLoopPlan decodes the issue's plan from its current metadata.
func (s *TaskService) outerLoopPlan(ctx context.Context, issue db.Issue) (outerloop.Plan, bool) {
	return outerloop.ParsePlan(util.JSONObjectOrEmpty(issue.Metadata))
}

// outerLoopSetMetaKey writes one metadata key through the atomic single-key
// path and mirrors the handler's metadata-changed event so live clients see
// server-side plan transitions.
func (s *TaskService) outerLoopSetMetaKey(ctx context.Context, issue db.Issue, key, rawValue string) {
	updated, err := s.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID, Key: key, Value: []byte(rawValue),
	})
	if err != nil {
		slog.Error("outer loop: plan metadata write failed",
			"issue_id", util.UUIDToString(issue.ID), "key", key, "error", err)
		return
	}
	if s.Bus != nil {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventIssueMetadataChanged,
			WorkspaceID: util.UUIDToString(issue.WorkspaceID),
			ActorType:   "system",
			ActorID:     "",
			Payload: map[string]any{
				"issue_id":       util.UUIDToString(updated.ID),
				"metadata":       util.JSONObjectOrEmpty(updated.Metadata),
				"issue_revision": updated.Revision,
			},
		})
	}
}

func (s *TaskService) outerLoopSystemComment(ctx context.Context, issue db.Issue, content string) {
	created, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		ID:          dbid.NewV7(),
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     content,
		Type:        "system",
		ParentID:    pgtype.UUID{},
	})
	if err != nil {
		slog.Warn("outer loop: system comment failed",
			"issue_id", util.UUIDToString(issue.ID), "error", err)
		return
	}
	if s.Bus != nil {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventCommentCreated,
			WorkspaceID: util.UUIDToString(issue.WorkspaceID),
			ActorType:   "system",
			ActorID:     "",
			Payload: map[string]any{
				"comment":     commentEventFields(created.Comment()),
				"issue_title": issue.Title,
			},
		})
	}
}

// outerLoopLastCommentIsMembers reports whether the issue's newest comment
// is member-authored — the 成员优先 gate.
func (s *TaskService) outerLoopLastCommentIsMembers(ctx context.Context, issue db.Issue) bool {
	recent, err := s.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       1,
	})
	if err != nil || len(recent) == 0 {
		return false
	}
	return recent[len(recent)-1].AuthorType == "member"
}

// outerLoopPlanAgentID resolves the agent the loop drives: the plan's pinned
// agent, falling back to the issue's agent assignee for plans opened before
// the pin existed.
func outerLoopPlanAgentID(issue db.Issue, plan outerloop.Plan) pgtype.UUID {
	if plan.AgentID != "" {
		if u, err := util.ParseUUID(plan.AgentID); err == nil {
			return u
		}
	}
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" {
		return issue.AssigneeID
	}
	return pgtype.UUID{}
}

// jsonString renders s as a JSON string literal (quotes included) for the
// metadata single-key writes, which take raw JSON values.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal on a string cannot fail; guarded for signature
		// honesty only.
		return `""`
	}
	return string(b)
}
