package handler

// RUYI-865 P1 fault-injection regressions: at every formal done entry point
// (HTTP single update, HTTP batch update, PR-merged advance) a transient
// failure of the transaction the done write settles in must leave no
// permanently-open card behind, and the retry/redelivery that follows must
// converge without duplicating the audit row or the decision:updated
// events. The failure is injected at txStarter.Begin — the exact seam where
// the P1 steady state ("done committed, cascade failed, no retry driver")
// used to form — so the tests prove the same-transaction design at each
// entry, not just in aggregate.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// faultyTxStarter stands in for Handler.TxStarter (the pool in the test
// fixture) and fails Begin while armed, mimicking the transient database
// failure the P1 cancel batch used to hit post-commit. It wraps the real
// txStarter so production wiring is otherwise untouched.
type faultyTxStarter struct {
	next txStarter
	mu   sync.Mutex
	fail bool
}

func (f *faultyTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail {
		return nil, errors.New("fault-injected: begin transaction failed")
	}
	return f.next.Begin(ctx)
}

// installFaultyTxStarter swaps the test handler's txStarter for the faulty
// wrapper for the duration of the test.
func installFaultyTxStarter(t *testing.T) *faultyTxStarter {
	t.Helper()
	f := &faultyTxStarter{next: testHandler.TxStarter}
	testHandler.TxStarter = f
	t.Cleanup(func() { testHandler.TxStarter = f.next })
	return f
}

// countDecisionUpdatedSubs subscribes to the bus once, per issue, and
// returns the total number of decision:updated events its issue emitted
// since the subscription. The bus has no unsubscribe, so each call owns a
// dedicated closure counter and the handler is harmless once its issue is
// cleaned up.
func countDecisionUpdatedSubs(t *testing.T, issueID string) *int32 {
	t.Helper()
	n := int32(0)
	testHandler.Bus.Subscribe(protocol.EventDecisionUpdated, func(e events.Event) {
		payload, _ := e.Payload.(map[string]any)
		if payload != nil && payload["issue_id"] == issueID {
			n++
		}
	})
	return &n
}

func issueStatusAndCards(t *testing.T, issueID string) (status string, openQuestionCards int) {
	t.Helper()
	row, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{
		ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load issue row %s: %v", issueID, err)
	}
	if err := testPool.QueryRow(t.Context(),
		`SELECT count(*) FROM issue_decisions
		 WHERE issue_id = $1 AND workspace_id = $2 AND status = 'open' AND decision_kind = 'question'`,
		parseUUID(issueID), parseUUID(testWorkspaceID)).Scan(&openQuestionCards); err != nil {
		t.Fatalf("count open question cards: %v", err)
	}
	return row.Status, openQuestionCards
}

func assertNoCascadeArtifacts(t *testing.T, issueID string, events *int32) {
	t.Helper()
	status, open := issueStatusAndCards(t, issueID)
	if status == "done" {
		t.Fatalf("issue %s is done after a failed settle: the transition must have rolled back with it", issueID)
	}
	if open != 2 {
		t.Errorf("issue %s open question cards after failure = %d, want 2 (no card may be stranded or half-cancelled)", issueID, open)
	}
	assertCascadeEvents(t, issueID, 0)
	if *events != 0 {
		t.Errorf("decision:updated events after failure = %d, want 0 (nothing committed, nothing published)", *events)
	}
}

// ── Entry point 1: HTTP single update ───────────────────────────────────────

func TestDoneCascadeFaultSingleUpdateConvergesOnRetry(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "fault single update done cascade issue")
	seedDecisionCards(t, issueID)
	fault := installFaultyTxStarter(t)
	events := countDecisionUpdatedSubs(t, issueID)

	// The done write settles its cascade in this request's transaction, so
	// Begin failing here must roll back the whole transition — the issue is
	// NOT done, its open cards survive, and nothing audit-shaped exists.
	fault.fail = true
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusInternalServerError {
		t.Fatalf("done update under injected Begin failure: expected 500, got %d: %s", w.Code, w.Body.String())
	}
	assertNoCascadeArtifacts(t, issueID, events)

	// The caller's retry of the same request converges: exactly one audit
	// row and exactly the flipped cards' events.
	fault.fail = false
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("retried done update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCascadeEvents(t, issueID, 1)
	if *events != 2 {
		t.Errorf("decision:updated events after retry = %d, want 2 (one per flipped card)", *events)
	}

	// The retry's own replay is a CAS no-op: no new audit row, no new
	// events.
	if w := updateIssueViaHandler(t, issueID, map[string]any{"status": "done"}); w.Code != http.StatusOK {
		t.Fatalf("repeated done update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertCascadeEvents(t, issueID, 1)
	if *events != 2 {
		t.Errorf("decision:updated events after re-done = %d, want still 2 (retries must not duplicate)", *events)
	}
}

// ── Entry point 2: HTTP batch update ────────────────────────────────────────

func TestDoneCascadeFaultBatchUpdateConvergesOnRetry(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "fault batch update done cascade issue")
	seedDecisionCards(t, issueID)
	fault := installFaultyTxStarter(t)
	events := countDecisionUpdatedSubs(t, issueID)

	batchDone := func() int {
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPost, "/api/issues/batch-update", map[string]any{
			"issue_ids": []string{issueID},
			"updates":   map[string]any{"status": "done"},
		})
		testHandler.BatchUpdateIssues(w, req)
		var batchResp struct {
			Updated int `json:"updated"`
		}
		if err := json.NewDecoder(w.Body).Decode(&batchResp); err != nil {
			t.Fatalf("decode batch response: %v", err)
		}
		return batchResp.Updated
	}

	// A failed item is skipped, not committed: the batch reports the item
	// as not updated and the transition rolled back with the settle.
	fault.fail = true
	if updated := batchDone(); updated != 0 {
		t.Fatalf("batch reported updated=%d under injected failure, want 0", updated)
	}
	assertNoCascadeArtifacts(t, issueID, events)

	// The client's retry of the skipped item converges.
	fault.fail = false
	if updated := batchDone(); updated != 1 {
		t.Fatalf("retried batch reported updated=%d, want 1", updated)
	}
	assertCascadeEvents(t, issueID, 1)
	if *events != 2 {
		t.Errorf("decision:updated events after retry = %d, want 2 (one per flipped card)", *events)
	}

	// Re-delivering the converged batch adds nothing to the ledger.
	if updated := batchDone(); updated != 1 {
		t.Fatalf("replayed batch reported updated=%d, want 1 (re-done is a valid write)", updated)
	}
	assertCascadeEvents(t, issueID, 1)
	if *events != 2 {
		t.Errorf("decision:updated events after replay = %d, want still 2 (retries must not duplicate)", *events)
	}
}

// ── Entry point 3: PR-merged advance (webhook redelivery) ──────────────────

const faultWebhookSecret = "fault-webhook-secret"

// ghMergedPRDelivery fires the same closed/merged pull_request webhook the
// github_test.go fixture does and returns the response status.
func ghMergedPRDelivery(t *testing.T, identifier string, prNumber int64) int {
	t.Helper()
	body := map[string]any{
		"action": "closed",
		"pull_request": map[string]any{
			"number":     prNumber,
			"html_url":   "https://github.com/acme/widget/pull/1234",
			"title":      "Fix login " + identifier,
			"body":       "Closes " + identifier,
			"state":      "closed",
			"draft":      false,
			"merged":     true,
			"merged_at":  "2026-04-29T00:00:00Z",
			"closed_at":  "2026-04-29T00:00:00Z",
			"created_at": "2026-04-28T00:00:00Z",
			"updated_at": "2026-04-29T00:00:00Z",
			"head":       map[string]any{"ref": "fix/login"},
			"user":       map[string]any{"login": "octocat", "avatar_url": ""},
		},
		"repository": map[string]any{
			"name":  "widget",
			"owner": map[string]any{"login": "acme"},
		},
		"installation": map[string]any{"id": 55667788},
	}
	raw, _ := json.Marshal(body)
	mac := hmac.New(sha256.New, []byte(faultWebhookSecret))
	mac.Write(raw)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/api/webhooks/github", bytes.NewReader(raw))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	testHandler.HandleGitHubWebhook(w, req)
	return w.Code
}

func TestDoneCascadeFaultWebhookRedeliveryConverges(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	issueID := dbfx.Issue(t, "fault webhook redelivery done cascade issue")
	identifier := getIssueViaHandler(t, issueID).Identifier
	seedDecisionCards(t, issueID)
	fault := installFaultyTxStarter(t)
	events := countDecisionUpdatedSubs(t, issueID)
	const installationID int64 = 55667788

	t.Setenv("GITHUB_WEBHOOK_SECRET", faultWebhookSecret)

	// The webhook attributes the PR to this installation (same id the
	// github_test.go fixture uses).
	if _, err := testHandler.Queries.CreateGitHubInstallation(t.Context(), db.CreateGitHubInstallationParams{
		WorkspaceID:    parseUUID(testWorkspaceID),
		InstallationID: installationID,
		AccountLogin:   "merge-sync-fault-acct",
		AccountType:    "User",
	}); err != nil {
		t.Fatalf("CreateGitHubInstallation: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(t.Context(), `DELETE FROM github_installation WHERE workspace_id = $1 AND installation_id = $2`,
			parseUUID(testWorkspaceID), installationID)
		testPool.Exec(t.Context(), `DELETE FROM issue_pull_request WHERE issue_id = $1`, parseUUID(issueID))
		testPool.Exec(t.Context(), `DELETE FROM github_pull_request WHERE workspace_id = $1`, parseUUID(testWorkspaceID))
	})

	// Delivery 1 fails inside the advance transaction: the webhook answers
	// 5xx so the provider re-delivers, and the transition is fully rolled
	// back — no done status, no stranded card, no audit row, no event.
	fault.fail = true
	if code := ghMergedPRDelivery(t, identifier, 1234); code != http.StatusInternalServerError {
		t.Fatalf("webhook delivery under injected Begin failure: expected 500, got %d", code)
	}
	assertNoCascadeArtifacts(t, issueID, events)

	// The provider's redelivery (fault cleared) is the retry driver: it
	// converges to exactly one audit row and two events.
	fault.fail = false
	if code := ghMergedPRDelivery(t, identifier, 1234); code != http.StatusAccepted {
		t.Fatalf("redelivered webhook: expected 202, got %d", code)
	}
	if status, open := issueStatusAndCards(t, issueID); status != "done" || open != 0 {
		t.Fatalf("after redelivery issue status=%q open cards=%d, want done / 0", status, open)
	}
	assertCascadeEvents(t, issueID, 1)
	if *events != 2 {
		t.Errorf("decision:updated events after redelivery = %d, want 2 (one per flipped card)", *events)
	}

	// A third delivery of the same event is a no-op replay: the terminal
	// gate skips the advance, and even if it settled again the CAS flips
	// nothing, so the ledger and the bus stay untouched.
	if code := ghMergedPRDelivery(t, identifier, 1234); code != http.StatusAccepted {
		t.Fatalf("replayed webhook: expected 202, got %d", code)
	}
	assertCascadeEvents(t, issueID, 1)
	if *events != 2 {
		t.Errorf("decision:updated events after replay = %d, want still 2 (retries must not duplicate)", *events)
	}
}
