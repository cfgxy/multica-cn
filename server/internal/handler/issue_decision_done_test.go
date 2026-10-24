package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
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
	// fixture pins its request-side wiring separately. The transition
	// (status write + card cascade) commits in one transaction, so a
	// failure here leaves neither a done status nor a half-cancelled
	// backlog behind.
	if err := testHandler.advanceIssueToDone(t.Context(), issueRow, testWorkspaceID); err != nil {
		t.Fatalf("advance to done: %v", err)
	}

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

// RUYI-865 P1 follow-up: the create-vs-done interleavings, pinned by
// holding each side's transaction open on its own connection so the row
// lock the protocol serialises on (issue row FOR UPDATE on the create
// side, conflicting with the done side's status UPDATE) is observed
// directly. waitForBlockedWriter proves a backend is genuinely parked on a
// lock, not merely slow.

// openCardCreateTx starts the create side's transaction on a dedicated
// connection and runs the two create-side statements in handler order
// (issue-row lock, then the guarded insert). The returned channels let the
// caller order the race deterministically: the create transaction is
// proven holding the row lock via the locked channel BEFORE the done side
// writes, and finish commits it (or returns the statement error) at the
// caller's chosen moment, which is when the blocked done write unblocks.
func openCardCreateTx(t *testing.T, ctx context.Context, params db.CreateIssueDecisionParams) (locked chan struct{}, finish func() error) {
	t.Helper()
	conn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire create connection: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()
		t.Fatalf("begin create transaction: %v", err)
	}
	stmtErr := make(chan error, 1)
	locked = make(chan struct{})
	qtx := db.New(tx)
	go func() {
		if _, err := qtx.LockIssueForDecisionCreate(ctx, db.LockIssueForDecisionCreateParams{
			ID: params.IssueID, WorkspaceID: params.WorkspaceID,
		}); err != nil {
			stmtErr <- err
			return
		}
		close(locked)
		if _, err := qtx.CreateIssueDecision(ctx, params); err != nil {
			stmtErr <- err
			return
		}
		stmtErr <- nil
	}()
	finish = func() error {
		if err := <-stmtErr; err != nil {
			_ = tx.Rollback(ctx)
			conn.Release()
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			conn.Release()
			return err
		}
		conn.Release()
		return nil
	}
	return locked, finish
}

// assertNoOpenQuestionCards is the P1 acceptance check: after a done
// transition has converged, no open question card survives on the issue.
func assertNoOpenQuestionCards(t *testing.T, issueID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*)::int FROM issue_decisions
		 WHERE issue_id = $1 AND status = 'open' AND decision_kind = 'question'`,
		parseUUID(issueID)).Scan(&n); err != nil {
		t.Fatalf("count open question cards: %v", err)
	}
	if n != 0 {
		t.Errorf("open question cards after done transition = %d, want 0 (RUYI-865 P1: no survivor)", n)
	}
}

// runDoneStatusUpdate runs the done side's transition on a dedicated
// connection and reports when it parked and when it landed: parked lets
// the caller assert the write is blocked on the create side's row lock,
// doneErr the transition's outcome once unblocked. It drives the SAME
// explicit-transaction shape the production entry points now use
// (RUYI-865 P1): one transaction that issues the status write, settles the
// card cascade inside it, and commits both — so the test's parked write is
// proven against the protocol the handlers actually run.
func runDoneStatusUpdate(ctx context.Context, t *testing.T, issueID string) (parked chan int32, doneErr chan error) {
	t.Helper()
	parked = make(chan int32, 1)
	doneErr = make(chan error, 1)
	go func() {
		conn, err := testPool.Acquire(ctx)
		if err != nil {
			doneErr <- err
			return
		}
		defer conn.Release()
		var pid int32
		if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			doneErr <- err
			return
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			doneErr <- err
			return
		}
		qdone := db.New(tx)
		parked <- pid
		issue, err := qdone.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
			ID: parseUUID(issueID), Status: "done", WorkspaceID: parseUUID(testWorkspaceID),
		})
		if err == nil {
			// Same settle the http entry point runs in this transaction:
			// the just-committed card is seen under a fresh READ COMMITTED
			// snapshot once the lock release unblocked the update.
			if _, err = testHandler.settleDoneCascadeInTx(ctx, qdone, issue, "http_update_issue"); err == nil {
				err = tx.Commit(ctx)
			}
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
		doneErr <- err
	}()
	return parked, doneErr
}

// create-commits-first: the card's create transaction holds the issue row
// lock, its card lands, and it commits before the done status write. The
// done write is proven parked on that lock, and once the card commits it
// proceeds — the status write and the in-tx settle that converges the
// just-committed card to cancelled with an audit row ride ONE transaction
// (the same shape the http entry points now run, RUYI-865 P1), so no open
// card survives the done transition. This is the P1 survivor interleaving:
// without the lock, the done write's transaction would miss a card that
// commits in between.
func TestIssueDoneCascadeCreateCommitsFirstConvergesLeftoverCard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := t.Context()
	issueID := dbfx.Issue(t, "done race create-first issue")

	locked, finish := openCardCreateTx(t, ctx, db.CreateIssueDecisionParams{
		ID:                 dbid.NewV7(),
		WorkspaceID:        parseUUID(testWorkspaceID),
		IssueID:            parseUUID(issueID),
		Question:           "raced card",
		Options:            []byte(`[{"label":"A"},{"label":"B"}]`),
		MultiSelect:        false,
		RecommendedIndices: []byte(`[]`),
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	<-locked // the create transaction holds the issue row lock

	// The done write on a second connection: it must park on the row
	// lock the create transaction holds.
	parked, doneErr := runDoneStatusUpdate(ctx, t, issueID)
	donePID := <-parked
	waitForBlockedWriter(t, ctx, donePID)

	if err := finish(); err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if err := <-doneErr; err != nil {
		t.Fatalf("done transition (status write + in-tx settle + commit): %v", err)
	}

	// The done write's transaction settled the just-committed card in-tx
	// (the same step the http entry points run on the transition, now in the
	// same commit): exactly one audit row, covering the raced card.
	assertNoOpenQuestionCards(t, issueID)
	if rows := assertCascadeEvents(t, issueID, 1); len(rows[0].DecisionIDs) != 1 {
		t.Errorf("settle audit decision_ids = %v, want exactly the raced card", rows[0].DecisionIDs)
	}
}

// done-commits-first: the done status write lands inside its own
// transaction and commits before the card's create transaction requests
// the row lock. The lock request is proven parked behind the uncommitted
// done write; after the done write commits the lock is granted and the
// guarded insert must refuse under its post-lock re-read — the card never
// enters the backlog.
func TestIssueDoneCascadeDoneCommitsFirstRefusesRacedCreate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := t.Context()
	issueID := dbfx.Issue(t, "done race done-first issue")

	// The done side: a write transaction that moves the issue to done but
	// has NOT committed yet, holding the row lock.
	doneConn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire done connection: %v", err)
	}
	defer doneConn.Release()
	doneTx, err := doneConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin done transaction: %v", err)
	}
	qdone := db.New(doneTx)
	if _, err := qdone.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: parseUUID(issueID), Status: "done", WorkspaceID: parseUUID(testWorkspaceID),
	}); err != nil {
		t.Fatalf("done status write: %v", err)
	}

	// The create side: its lock request must park behind the uncommitted
	// done write; prove it via pg_stat_activity on the create backend.
	createConn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire create connection: %v", err)
	}
	defer createConn.Release()
	createTx, err := createConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin create transaction: %v", err)
	}
	defer createTx.Rollback(ctx)
	createPID := backendPID(t, ctx, createConn)
	qcreate := db.New(createTx) // statements ride the transaction's connection
	locked := make(chan error, 1)
	go func() {
		_, lerr := qcreate.LockIssueForDecisionCreate(ctx, db.LockIssueForDecisionCreateParams{
			ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
		})
		locked <- lerr
	}()
	waitForBlockedWriter(t, ctx, createPID) // the lock request is parked

	if err := doneTx.Commit(ctx); err != nil {
		t.Fatalf("commit done transaction: %v", err)
	}
	if err := <-locked; err != nil {
		t.Fatalf("create-side lock request after done commit: %v", err)
	}

	// The guarded insert now runs with its lock granted: the post-lock
	// re-read sees done and refuses the card.
	created, err := qcreate.CreateIssueDecision(ctx, db.CreateIssueDecisionParams{
		ID:                 dbid.NewV7(),
		WorkspaceID:        parseUUID(testWorkspaceID),
		IssueID:            parseUUID(issueID),
		Question:           "raced card",
		Options:            []byte(`[{"label":"A"},{"label":"B"}]`),
		MultiSelect:        false,
		RecommendedIndices: []byte(`[]`),
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("guarded insert on a done issue = %v, want the done guard's 0-row miss (RUYI-865 P1 survivor)", err)
	}
	_ = created
	if err := createTx.Commit(ctx); err != nil {
		t.Fatalf("commit create transaction: %v", err)
	}
	assertNoOpenQuestionCards(t, issueID)
}

// backendPID reports the server pid of a pool connection, so a test can
// prove that connection is parked on a lock via pg_stat_activity.
func backendPID(t *testing.T, ctx context.Context, conn *pgxpool.Conn) int32 {
	t.Helper()
	var pid int32
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("pg_backend_pid: %v", err)
	}
	return pid
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
