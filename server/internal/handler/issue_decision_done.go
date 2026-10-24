package handler

// RUYI-865: when an issue formally enters the done category, its still-open
// question decision cards are auto-cancelled server-side so they stop
// holding decision-center backlog slots. This file owns that cascade. The
// settle half runs INSIDE the done write's own transaction
// (settleDoneCascadeInTx); every formal done entry point (HTTP single
// update, HTTP batch update, and the PR-merged advance) runs its status
// write and the settle in one transaction so the behavior can never drift
// across entries — and a failed settle can never commit a done status
// without cancelling the cards.

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	// decisionAutoCancelReason is the human-facing cancel reason recorded in
	// the audit row, per the RUYI-865 rule "Issue 已完成，系统自动取消".
	decisionAutoCancelReason = "Issue 已完成，系统自动取消"
	// decisionAutoCancelSource names this cascade in the audit details so a
	// reviewer can distinguish it from a member-initiated cancel.
	decisionAutoCancelSource = "issue_done_cascade"
)

// settleDoneCascadeInTx settles the done-side half of the create-vs-done race
// (RUYI-865) INSIDE the caller's already-open transaction, on the caller's
// qtx, and returns the cards it flipped. Any returned error rolls back the
// whole done transition with it — this is the P1 close: the post-commit
// best-effort version committed the status write first and left stranded
// open cards plus no automatic retry when the cancel batch failed. The
// entry points are the HTTP single/batch write paths (their status-guard or
// atomic update transaction, which a done target now always takes) and the
// PR-merged advance (its own write transaction); each propagates the error
// to the request — the webhooks answer 5xx, so the provider re-delivery is
// the retry driver, and the HTTP write's caller retries the same request.
//
// Both halves are idempotent under retry, so a redelivery after a committed
// failure converges instead of duplicating:
//
//   - ONE CAS batch (CancelOpenIssueDecisionsForIssue): it only flips rows
//     still in status='open', so a repeated Done write, a webhook replay,
//     or a re-opened issue finding itself done again converges to the same
//     state.
//   - answered and cancelled cards are never touched, on purpose: an in-
//     flight answer that wins its own CAS before this batch keeps its
//     'answered' state (the human decided), and a previously cancelled card
//     must not resurrect when the issue re-enters done (RUYI-865 rule 4).
//   - authorization cards keep their decision_requests lifecycle and are
//     excluded by the CAS predicate (decision_kind = 'question').
//   - the audit row is written ONLY when cards actually flipped (and in the
//     same transaction as the flips). A no-op replay therefore appends
//     nothing and publishes nothing: retries can never duplicate audit
//     rows or decision:updated events.
//
// Why the in-tx settle still closes the create-vs-done race: the card-create
// side holds the issue row FOR UPDATE inside its own transaction
// (LockIssueForDecisionCreate), and the done write's status UPDATE locks the
// same row. Either the create commits first — its card is visible to this
// transaction's fresh READ COMMITTED read, the CAS flips it before commit,
// no survivor — or the done write commits first and the create's post-lock
// re-read refuses the card with a 409.
func (h *Handler) settleDoneCascadeInTx(ctx context.Context, q *db.Queries, oldIssue, newIssue db.Issue, entrySource string) ([]db.IssueDecision, error) {
	if !oldIssue.ID.Valid || !newIssue.ID.Valid {
		// Without both rows, the transaction cannot prove a status transition.
		return nil, nil
	}
	// Settle legacy cards only when this write moves the issue into the done
	// category, including custom statuses (MUL-6243). Metadata edits, repeated
	// done writes, and transitions out of done must not backfill history.
	if issuestatus.Effective(ctx, q, oldIssue.WorkspaceID, oldIssue.Status) == issuestatus.Done ||
		issuestatus.Effective(ctx, q, newIssue.WorkspaceID, newIssue.Status) != issuestatus.Done {
		return nil, nil
	}
	flipped, err := q.CancelOpenIssueDecisionsForIssue(ctx, db.CancelOpenIssueDecisionsForIssueParams{
		IssueID:     newIssue.ID,
		WorkspaceID: newIssue.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	if len(flipped) == 0 {
		return nil, nil
	}

	details, _ := json.Marshal(map[string]any{
		"reason":       decisionAutoCancelReason,
		"source":       decisionAutoCancelSource,
		"entry_source": entrySource,
		"decision_ids": mapIDs(flipped),
	})
	// The audit row is part of the business guarantee, not a traceability
	// add-on: it commits with the flips or the transition rolls back.
	if err := service.AppendAuditEvents(ctx, q, service.IssueEvent(
		service.AuditIssueDecisionCardsAutoCancelled, service.AuditActorSystem, pgtype.UUID{},
		newIssue.WorkspaceID, newIssue.ID, details)); err != nil {
		return nil, err
	}
	return flipped, nil
}

// publishDoneCascade emits the post-commit realtime half of the cascade: one
// decision:updated per flipped card, so issue browsers and the workspace
// decision inbox (RUYI-494) re-bucket and drop the todo badge without a
// refetch. It is bound to the cards the in-tx settle actually returned, so
// a no-op replay or a write that touched no card publishes nothing — synced
// cards never duplicate either. It runs after the transaction has
// committed, so a failure here cannot undo the settled state; clients
// refetch, and the log line keeps the ids.
func (h *Handler) publishDoneCascade(ctx context.Context, issue db.Issue, cards []db.IssueDecision) {
	for _, card := range cards {
		h.publish(protocol.EventDecisionUpdated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
			"decision":    decisionToResponse(card),
			"issue_id":    uuidToString(issue.ID),
			"issue_title": issue.Title,
		})
	}
	if len(cards) > 0 {
		slog.Info("issue done cascade: open decision cards auto-cancelled",
			"issue_id", uuidToString(issue.ID), "workspace_id", uuidToString(issue.WorkspaceID),
			"cancelled", len(cards))
	}
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
