package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-865: when an issue formally enters the done category, the server
// cancels every still-open question decision card in one idempotent batch,
// records an auditable system event, and never resurrects cancelled cards on
// re-entry. The canonical assertions live here, on the shared cascade
// helper exercised through each formal entry point (HTTP single update,
// batch update, the PR-merged advance) plus the create-side race guard.

const decisionAutoCancelEvent = "issue.decision_cards_auto_cancelled"

// autoCancelAuditRow is one audit_event row written by the cascade.
type autoCancelAuditRow struct {
	EventType   string
	Reason      string
	Source      string
	EntrySource string
	DecisionIDs []string
}

// assertCascadeEvents reads the cascade's audit rows for an issue and
// returns them, so each entry-point test asserts on the same ledger shape.
// An idempotent no-op transition (no open card left to flip) writes NO row,
// so wantRows is the number of fired transitions, not of card flips.
func assertCascadeEvents(t *testing.T, issueID string, wantRows int) []autoCancelAuditRow {
	t.Helper()
	rows, err := testPool.Query(t.Context(), `
		SELECT event_type, details
		FROM audit_event
		WHERE issue_id = $1 AND event_type = $2
		ORDER BY id ASC
	`, parseUUID(issueID), decisionAutoCancelEvent)
	if err != nil {
		t.Fatalf("query cascade audit rows: %v", err)
	}
	defer rows.Close()
	var out []autoCancelAuditRow
	for rows.Next() {
		var ev string
		var details []byte
		if err := rows.Scan(&ev, &details); err != nil {
			t.Fatalf("scan cascade audit row: %v", err)
		}
		var body struct {
			Reason      string   `json:"reason"`
			Source      string   `json:"source"`
			EntrySource string   `json:"entry_source"`
			DecisionIDs []string `json:"decision_ids"`
		}
		if err := json.Unmarshal(details, &body); err != nil {
			t.Fatalf("cascade audit details are not JSON: %v: %s", err, details)
		}
		out = append(out, autoCancelAuditRow{
			EventType:   ev,
			Reason:      body.Reason,
			Source:      body.Source,
			EntrySource: body.EntrySource,
			DecisionIDs: body.DecisionIDs,
		})
	}
	if len(out) != wantRows {
		t.Fatalf("cascade audit rows = %d, want %d: %+v", len(out), wantRows, out)
	}
	return out
}

// seedDecisionCards inserts a mixed card set on the issue: two open question
// cards, one answered question card, one cancelled question card and one
// open authorization card, returning each id for assertions.
func seedDecisionCards(t *testing.T, issueID string) (openA, openB, answered, cancelled, auth string) {
	t.Helper()
	openA = insertInboxCard(t, testWorkspaceID, issueID, "open A", "open", "5s ago")
	openB = insertInboxCard(t, testWorkspaceID, issueID, "open B", "open", "4s ago")
	answered = insertInboxCard(t, testWorkspaceID, issueID, "answered", "answered", "6s ago",
		testutil.Cols{
			"selected_indices": testutil.Raw(`'[0]'::jsonb`),
			"answered_by_type": "member",
			"answered_by_id":   testUserID,
			"answered_at":      testutil.Raw("now() - interval '5 seconds'"),
		})
	cancelled = insertInboxCard(t, testWorkspaceID, issueID, "pre-cancelled", "cancelled", "7s ago")
	agentID := createHandlerTestAgent(t, "done-cascade-auth-agent", nil)
	groupID := uuidToString(dbid.NewV7())
	auth = insertInboxCard(t, testWorkspaceID, issueID, "authorization card", "open", "3s ago",
		testutil.Cols{
			"decision_kind":            "authorization",
			"visible_tier":             "members",
			"operator_tier":            "owner",
			"named_approver_ids":       testutil.Raw(`'[]'::jsonb`),
			"auth_state":               "pending",
			"pending_request_group_id": groupID,
			"pending_action_type":      "prompt_restore",
			"pending_action_params":    testutil.Raw(`'{}'::jsonb`),
			"expires_at":               testutil.Raw("now() + interval '1 hour'"),
			"created_by_type":          "agent",
			"created_by_id":            agentID,
		})
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM issue_decisions WHERE workspace_id = $1 AND issue_id = $2`,
			testWorkspaceID, parseUUID(issueID))
		testPool.Exec(context.Background(),
			`DELETE FROM audit_event WHERE issue_id = $1 AND event_type = $2`,
			parseUUID(issueID), decisionAutoCancelEvent)
	})
	return
}

// assertCardStatuses verifies the per-card outcome set after a done
// transition: open question cards flipped to cancelled, everything else
// untouched.
func assertCardStatuses(t *testing.T, cards map[string]string) {
	t.Helper()
	for id, expected := range cards {
		var got string
		if err := testPool.QueryRow(t.Context(),
			`SELECT status FROM issue_decisions WHERE id = $1`, parseUUID(id)).Scan(&got); err != nil {
			t.Fatalf("status of card %s: %v", id, err)
		}
		if got != expected {
			t.Errorf("card %s status = %q, want %q", id, got, expected)
		}
	}
}

// statusCardsAfterDone is the expected per-card outcome map: open question
// cards auto-cancelled, the answered / pre-cancelled question cards and the
// open authorization card untouched.
func statusCardsAfterDone(t *testing.T, openA, openB, answered, cancelled, auth string) map[string]string {
	t.Helper()
	return map[string]string{
		openA: "cancelled", openB: "cancelled",
		answered: "answered", cancelled: "cancelled", auth: "open",
	}
}

// statusCardsPreDone is the same map for the no-transition state: nothing
// has been flipped yet.
func statusCardsPreDone(t *testing.T, openA, openB, answered, cancelled, auth string) map[string]string {
	t.Helper()
	return map[string]string{
		openA: "open", openB: "open",
		answered: "answered", cancelled: "cancelled", auth: "open",
	}
}

func TestIssueDoneCascadeAutoCancelsOpenDecisionCards(t *testing.T) {
	issueID := dbfx.Issue(t, "done cascade issue")
	openA, openB, answered, cancelled, auth := seedDecisionCards(t, issueID)

	// Non-done transitions must not fire: the open cards survive and no
	// cascade audit row is written.
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "in_review"}); w.Code != http.StatusOK {
		t.Fatalf("in_review update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCardStatuses(t, statusCardsPreDone(t, openA, openB, answered, cancelled, auth))
	assertCascadeEvents(t, issueID, 0)

	// Entering done flips exactly the open question cards and records the
	// auditable reason/source/ids.
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("done update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCardStatuses(t, statusCardsAfterDone(t, openA, openB, answered, cancelled, auth))
	events := assertCascadeEvents(t, issueID, 1)
	ev := events[0]
	if ev.Reason != "Issue 已完成，系统自动取消" {
		t.Errorf("audit reason = %q, want the fixed auto-cancel text", ev.Reason)
	}
	if ev.Source != "issue_done_cascade" || ev.EntrySource != "http_update_issue" {
		t.Errorf("audit source/entry = %q/%q, want issue_done_cascade/http_update_issue", ev.Source, ev.EntrySource)
	}
	got := map[string]bool{}
	for _, id := range ev.DecisionIDs {
		got[id] = true
	}
	if len(ev.DecisionIDs) != 2 || !got[openA] || !got[openB] {
		t.Errorf("audit decision_ids = %v, want exactly the two flipped cards [%s %s]", ev.DecisionIDs, openA, openB)
	}

	// Reopening the issue must not resurrect the auto-cancelled cards, and
	// the non-done transition writes no audit row.
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "in_progress"}); w.Code != http.StatusOK {
		t.Fatalf("re-open: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCardStatuses(t, statusCardsAfterDone(t, openA, openB, answered, cancelled, auth))
	assertCascadeEvents(t, issueID, 1)

	// A repeated formal done entry fires the transition again but flips no
	// card (idempotent CAS), so it appends no audit row: the ledger keeps
	// only the original two-card event.
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("second done: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCardStatuses(t, statusCardsAfterDone(t, openA, openB, answered, cancelled, auth))
	assertCascadeEvents(t, issueID, 1)
}

func TestIssueDoneCascadeBatchUpdateEntry(t *testing.T) {
	issueID := dbfx.Issue(t, "batch done cascade issue")
	openA, openB, answered, cancelled, auth := seedDecisionCards(t, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issueID},
		"updates":   map[string]any{"status": "done"},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCardStatuses(t, statusCardsAfterDone(t, openA, openB, answered, cancelled, auth))
	events := assertCascadeEvents(t, issueID, 1)
	if events[0].EntrySource != "http_batch_update_issues" {
		t.Errorf("audit entry_source = %q, want http_batch_update_issues", events[0].EntrySource)
	}
	if len(events[0].DecisionIDs) != 2 {
		t.Errorf("batch audit decision_ids = %v, want the two open cards", events[0].DecisionIDs)
	}
}

func TestIssueDoneCascadePRMergedEntry(t *testing.T) {
	issueID := dbfx.Issue(t, "pr-merged done cascade issue")
	openA, openB, answered, cancelled, auth := seedDecisionCards(t, issueID)

	issueRow, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{
		ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load issue row: %v", err)
	}
	// Drive the shared PR-merged path directly; github_test.go's webhook
	// fixture pins its request-side wiring separately.
	testHandler.advanceIssueToDone(t.Context(), issueRow, testWorkspaceID)

	assertCardStatuses(t, statusCardsAfterDone(t, openA, openB, answered, cancelled, auth))
	events := assertCascadeEvents(t, issueID, 1)
	if events[0].EntrySource != "github_pr_merged" {
		t.Errorf("audit entry_source = %q, want github_pr_merged", events[0].EntrySource)
	}
}

func TestDecisionCreateRefusedOnDoneIssue(t *testing.T) {
	f := newDecisionFixture(t)
	issueID := f.IssueID
	insertInboxCard(t, testWorkspaceID, issueID, "probe card", "open", "")
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("done update: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// A new card cannot land after the done transition.
	w := createDecisionCard(t, f, false, map[string]any{
		"question": "late card", "options": []string{"A", "B"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("create on done issue: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var openCount int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*)::int FROM issue_decisions WHERE issue_id = $1 AND status = 'open' AND decision_kind = 'question'`,
		parseUUID(issueID)).Scan(&openCount); err != nil {
		t.Fatalf("count open cards: %v", err)
	}
	if openCount != 0 {
		t.Errorf("open question cards after refused create = %d, want 0", openCount)
	}

	// Reopening lifts the refusal: the same card shape creates fine.
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "todo"}); w.Code != http.StatusOK {
		t.Fatalf("re-open: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	w = createDecisionCard(t, f, false, map[string]any{
		"question": "fresh card", "options": []string{"A", "B"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create after re-open: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDecisionCreateRefusedOnCustomDoneKey pins the create-side race guard on
// a CUSTOM done status: a card raised while the issue sits on a custom
// done-category key is refused atomically by the insert's own guard, and the
// 409 re-check resolves against a fresh issue read (the request-start
// snapshot would be exactly the stale row this race produces).
func TestDecisionCreateRefusedOnCustomDoneKey(t *testing.T) {
	f := newDecisionFixture(t)
	issueID := f.IssueID
	agentID := createHandlerTestAgent(t, "custom-done-auth-agent", nil)

	ctx := t.Context()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	key := "qa_done_" + suffix
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_status (workspace_id, key, name, description, category, color, position)
		 VALUES ($1, $2, 'QA Done', '', 'done', '#00ff00', 99)`,
		testWorkspaceID, key); err != nil {
		t.Fatalf("seed custom done status: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM issue_status WHERE workspace_id = $1 AND key = $2`, testWorkspaceID, key)
	})

	// A custom done-category status must fire the cascade exactly like the
	// built-in 'done'.
	openA := insertInboxCard(t, testWorkspaceID, issueID, "custom done open card", "open", "1s ago")
	authCard := insertInboxCard(t, testWorkspaceID, issueID, "custom done auth card", "open", "1s ago",
		testutil.Cols{
			"decision_kind":            "authorization",
			"auth_state":               "pending",
			"pending_request_group_id": uuidToString(dbid.NewV7()),
			"pending_action_type":      "prompt_restore",
			"created_by_type":          "agent",
			"created_by_id":            agentID,
		})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"status": key}), "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("custom done update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCardStatuses(t, map[string]string{openA: "cancelled", authCard: "open"})
	assertCascadeEvents(t, issueID, 1)

	// A card raised on the custom done status is refused with the same 409
	// contract; reopening lifts it again.
	w = createDecisionCard(t, f, false, map[string]any{
		"question": "late card on custom done", "options": []string{"A", "B"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("create on custom done issue: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE issue SET status = 'todo' WHERE id = $1`, parseUUID(issueID)); err != nil {
		t.Fatalf("re-open issue: %v", err)
	}
	w = createDecisionCard(t, f, false, map[string]any{
		"question": "fresh card", "options": []string{"A", "B"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create after re-open: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

// TestIssueDoneCascadeRecoveryAfterMissedTransition pins the failure-retry
// posture: the cascade is a post-commit best-effort step, so the failure
// mode is "the issue is done but some cards were not cancelled yet" (e.g.
// the append happened after a server crash, or the cards were re-opened by
// a concurrent flow). The next formal done transition on that issue must
// converge the state — no backfill job, no dedicated retry mechanism.
func TestIssueDoneCascadeRecoveryAfterMissedTransition(t *testing.T) {
	issueID := dbfx.Issue(t, "cascade recovery issue")
	openA, openB, answered, cancelled, auth := seedDecisionCards(t, issueID)

	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("done: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// Force the missed-cascade state: the issue is done but two cards are
	// open again (simulate the cancel batch failing mid-flight).
	for _, id := range []string{openA, openB} {
		if _, err := testPool.Exec(t.Context(),
			`UPDATE issue_decisions SET status = 'open' WHERE id = $1`, parseUUID(id)); err != nil {
			t.Fatalf("re-open card: %v", err)
		}
	}

	// Re-open then re-done: the transition guard re-fires the cascade and
	// the CAS batch converges the leftover open cards, while the answered
	// and previously cancelled cards stay where they are.
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "in_progress"}); w.Code != http.StatusOK {
		t.Fatalf("re-open: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("second done: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCardStatuses(t, statusCardsAfterDone(t, openA, openB, answered, cancelled, auth))
	// Two fired transitions: the first wrote one row (two flipped ids),
	// the recovery transition wrote a second row covering the leftovers.
	events := assertCascadeEvents(t, issueID, 2)
	if len(events[1].DecisionIDs) != 2 {
		t.Errorf("recovery transition decision_ids = %v, want both leftover cards", events[1].DecisionIDs)
	}
}
