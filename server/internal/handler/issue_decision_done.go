package handler

// RUYI-865: when an issue formally enters the done category, its still-open
// question decision cards are auto-cancelled server-side so they stop
// holding decision-center backlog slots. This file owns that cascade; every
// formal done entry point (HTTP single update, HTTP batch update, and the
// PR-merged advance path) runs it through cancelOpenDecisionCardsForDone so
// the behavior can never drift across entries.

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/protocol"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// decisionAutoCancelReason is the human-facing cancel reason recorded in
	// the audit row, per the RUYI-865 rule "Issue 已完成，系统自动取消".
	decisionAutoCancelReason = "Issue 已完成，系统自动取消"
	// decisionAutoCancelSource names this cascade in the audit details so a
	// reviewer can distinguish it from a member-initiated cancel.
	decisionAutoCancelSource = "issue_done_cascade"
)

// cancelOpenDecisionCardsForDone cancels every still-open question decision
// card of an issue that just entered the done category, and records the
// transition as an auditable, self-describing system event:
//
//   - ONE CAS batch (CancelOpenIssueDecisionsForIssue): atomic, and because
//     it only flips rows still in status='open' it is idempotent — a replay
//     after a committed failure, a repeated Done write, or a re-opened
//     issue finding itself done again converges to the same state.
//   - answered and cancelled cards are never touched, on purpose: an in-
//     flight answer that wins its own CAS before this batch keeps its
//     'answered' state (the human decided), and a previously cancelled card
//     must not resurrect when the issue re-enters done (RUYI-865 rule 4).
//   - authorization cards keep their decision_requests lifecycle and are
//     excluded by the CAS predicate (decision_kind = 'question').
//   - a best-effort issue-domain audit row carries the reason, the entry
//     source, and the exact card ids so the auto-cancellation is traceable
//     even though the cards themselves only carry status='cancelled'.
//   - one decision:updated publish per flipped card so issue browsers and
//     the workspace decision inbox (RUYI-494) re-bucket and drop the todo
//     badge without a refetch.
//
// The cascade runs post-commit, best-effort, like the other done-entry
// side effects: a failure is logged with the ids and retried by the next
// formal done transition — it never fails the issue write that already
// committed.
func (h *Handler) cancelOpenDecisionCardsForDone(ctx context.Context, prevIssue, issue db.Issue, entrySource string) {
	// Transition guard: a move INTO the done category only. prevIssue is
	// the pre-write snapshot the caller read in the same request.
	if !h.enteredDoneCategory(ctx, prevIssue, issue) {
		return
	}
	cancelled, err := h.Queries.CancelOpenIssueDecisionsForIssue(ctx, db.CancelOpenIssueDecisionsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		slog.Error("issue done cascade: auto-cancel open decision cards failed",
			"issue_id", uuidToString(issue.ID), "workspace_id", uuidToString(issue.WorkspaceID),
			"entry_source", entrySource, "error", err)
		return
	}
	if len(cancelled) == 0 {
		return
	}

	details, _ := json.Marshal(map[string]any{
		"reason":       decisionAutoCancelReason,
		"source":       decisionAutoCancelSource,
		"entry_source": entrySource,
		"decision_ids": mapIDs(cancelled),
	})
	// The cancel itself is the committed business fact; the audit row is the
	// traceability add-on, appended best-effort through the shared outbox
	// discipline (a failed append logs, the cards stay cancelled).
	service.TryAppendAuditEvents(ctx, h.Queries, service.IssueEvent(
		service.AuditIssueDecisionCardsAutoCancelled, service.AuditActorSystem, pgtype.UUID{},
		issue.WorkspaceID, issue.ID, details))

	for _, card := range cancelled {
		h.publish(protocol.EventDecisionUpdated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
			"decision":    decisionToResponse(card),
			"issue_id":    uuidToString(issue.ID),
			"issue_title": issue.Title,
		})
	}
	slog.Info("issue done cascade: open decision cards auto-cancelled",
		"issue_id", uuidToString(issue.ID), "workspace_id", uuidToString(issue.WorkspaceID),
		"entry_source", entrySource, "cancelled", len(cancelled))
}

// mapIDs renders the card ids of a flipped batch into the audit details.
func mapIDs(cards []db.IssueDecision) []string {
	ids := make([]string, 0, len(cards))
	for _, c := range cards {
		ids = append(ids, uuidToString(c.ID))
	}
	return ids
}

// decisionCardsAllowedWhileDone reports whether a NEW decision card may be
// raised on the issue right now. Used by the create endpoint so a card
// racing the done transition cannot land AFTER the cascade and re-open the
// backlog the cleanup just cleared.
func (h *Handler) decisionCardsAllowedWhileDone(ctx context.Context, issue db.Issue) bool {
	return issuestatus.Effective(ctx, h.Queries, issue.WorkspaceID, issue.Status) != issuestatus.Done
}
