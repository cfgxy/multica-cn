package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/legislation"
)

func TestEnactRecoveryRejectsStructuredStaticTarget(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)
	withJevAdvisory(t, &legislation.AdvisoryClient{})

	initialID := createDraft(t, wsID, memberID, "恢复修订条款", cleanClauseFor("恢复修订条款"))
	submitDraft(t, wsID, memberID, initialID)
	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	code, body := legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+initialID+"/approve",
			map[string]any{"confirm_diff_previewed": true}, "id", initialID))
	if code != http.StatusOK {
		t.Fatalf("create clause: expected 200, got %d: %s", code, body)
	}
	var clauseID string
	dbfx.QueryRow(t, `SELECT clause_id::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, initialID).Scan(&clauseID)

	revisionID := createDraft(t, wsID, memberID, "恢复修订条款", cleanClauseFor("恢复修订条款"))
	submitDraft(t, wsID, memberID, revisionID)
	dbfx.Exec(t, `UPDATE prompt_proposal
		SET status = 'approved', enactment_target = 'static_prompt', change_kind = 'revise_clause', clause_id = $3
		WHERE workspace_id = $1 AND id = $2`, wsID, revisionID, clauseID)

	var carrierBefore string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&carrierBefore)
	ownerEnact := ownerRoute(http.HandlerFunc(testHandler.EnactPromptProposal))
	code, body = legislationCall(t, ownerEnact.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+revisionID+"/enact", nil, "id", revisionID))
	if code != http.StatusConflict {
		t.Fatalf("structured static recovery: expected 409, got %d: %s", code, body)
	}
	if got := proposalStatus(t, wsID, revisionID); got != "approved" {
		t.Fatalf("invalid recovery changed proposal status to %q", got)
	}
	var clauseVersions int
	dbfx.QueryRow(t, `SELECT COUNT(*) FROM clause_version WHERE clause_id = $1`, clauseID).Scan(&clauseVersions)
	if clauseVersions != 1 {
		t.Fatalf("invalid recovery wrote %d clause versions, want only the original", clauseVersions)
	}
	var carrierAfter string
	dbfx.QueryRow(t, `SELECT context FROM workspace WHERE id = $1`, wsID).Scan(&carrierAfter)
	if carrierAfter != carrierBefore {
		t.Fatal("invalid recovery changed the static carrier")
	}
}

func TestSubmitRevisionCapturesBaselineAfterConcurrentEnact(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)
	withJevAdvisory(t, &legislation.AdvisoryClient{})

	initialID := createDraft(t, wsID, memberID, "并发修订条款", cleanClauseFor("并发修订条款"))
	submitDraft(t, wsID, memberID, initialID)
	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	if code, body := legislationCall(t, ownerApprove.ServeHTTP,
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+initialID+"/approve",
			map[string]any{"confirm_diff_previewed": true}, "id", initialID)); code != http.StatusOK {
		t.Fatalf("create clause: expected 200, got %d: %s", code, body)
	}
	var clauseID string
	dbfx.QueryRow(t, `SELECT clause_id::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, initialID).Scan(&clauseID)

	revisionID := createDraft(t, wsID, memberID, "并发修订条款", cleanClauseFor("并发修订条款"))
	dbfx.Exec(t, `UPDATE prompt_proposal SET change_kind = 'revise_clause', clause_id = $3 WHERE workspace_id = $1 AND id = $2`, wsID, revisionID, clauseID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin enact transaction: %v", err)
	}
	defer blocker.Rollback(context.Background())
	var blockerPID int32
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read enact backend pid: %v", err)
	}
	if err := lockClauseReferenceWorkspaceTx(ctx, testHandler.Queries.WithTx(blocker), parseUUID(wsID)); err != nil {
		t.Fatalf("lock clause references: %v", err)
	}

	type response struct {
		code int
		body string
	}
	result := make(chan response, 1)
	req := legislationReq(memberID, wsID, http.MethodPost,
		"/api/prompt-legislation/proposals/"+revisionID+"/submit", nil, "id", revisionID)
	req = req.WithContext(ctx)
	go func() {
		w := httptest.NewRecorder()
		testHandler.SubmitPromptProposal(w, req)
		result <- response{code: w.Code, body: w.Body.String()}
	}()
	waitForBlockedDatabaseQuery(t, ctx, "pg_advisory_xact_lock", blockerPID)

	if _, err := blocker.Exec(ctx, `INSERT INTO clause_version (
		clause_id, version, content, content_sha256, source, carrier_scope, carrier_version, created_by_type, created_by_id
	) SELECT clause_id, 2, content, content_sha256, source, carrier_scope, carrier_version, created_by_type, created_by_id
	FROM clause_version WHERE clause_id = $1 AND version = 1`, clauseID); err != nil {
		t.Fatalf("advance clause version in enact transaction: %v", err)
	}
	if _, err := blocker.Exec(ctx, `UPDATE clause_binding SET since_clause_version = 2 WHERE clause_id = $1 AND status = 'active'`, clauseID); err != nil {
		t.Fatalf("advance active binding in enact transaction: %v", err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("commit enact transaction: %v", err)
	}

	select {
	case got := <-result:
		if got.code != http.StatusOK {
			t.Fatalf("submit after enact: expected 200, got %d: %s", got.code, got.body)
		}
	case <-ctx.Done():
		t.Fatalf("submit did not finish after enact commit: %v", ctx.Err())
	}

	var auditJSON string
	dbfx.QueryRow(t, `SELECT audit_log::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, revisionID).Scan(&auditJSON)
	baseline, ok := clauseRevisionBaselineFromAudit([]byte(auditJSON))
	if !ok || baseline.ClauseVersion != 2 || baseline.SinceClauseVersion != 2 {
		t.Fatalf("submitted baseline = %+v, ok=%v; want version 2 after enact, audit=%s", baseline, ok, auditJSON)
	}
}

func TestSubmitAdvisoryRunsOutsideProposalLockAndRejectsStaleInput(t *testing.T) {
	wsID, _, memberID := legislationFixture(t)
	id := createDraft(t, wsID, memberID, "慢 advisory 草案", cleanClauseFor("慢 advisory 草案"))
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task":"kev_local","p_success":0.8,"p_failure":0.2,"threshold":0.3,"warn":false,"decision":"pass","engine":"test","threshold_source":"default"}`))
	}))
	defer server.Close()
	withJevAdvisory(t, &legislation.AdvisoryClient{BaseURL: server.URL, Timeout: 10 * time.Second})

	type response struct {
		code int
		body string
	}
	result := make(chan response, 1)
	req := legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+id+"/submit", nil, "id", id)
	go func() {
		w := httptest.NewRecorder()
		testHandler.SubmitPromptProposal(w, req)
		result <- response{code: w.Code, body: w.Body.String()}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("submit advisory request did not start")
	}

	updateDone := make(chan error, 1)
	go func() {
		_, err := testPool.Exec(context.Background(), `UPDATE prompt_proposal SET clause_text = $3
			WHERE workspace_id = $1 AND id = $2 AND status = 'draft'`, wsID, id, cleanClauseFor("已并发编辑"))
		updateDone <- err
	}()
	select {
	case err := <-updateDone:
		if err != nil {
			close(release)
			t.Fatalf("concurrent draft edit while advisory runs: %v", err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		<-result
		t.Fatal("draft edit blocked while the advisory service was responding")
	}
	close(release)
	select {
	case got := <-result:
		if got.code != http.StatusConflict || !strings.Contains(got.body, "proposal changed") {
			t.Fatalf("submit with stale advisory input = %d %s, want conflict", got.code, got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("submit did not finish after advisory response")
	}
	if got := proposalStatus(t, wsID, id); got != "draft" {
		t.Fatalf("stale advisory submission changed status to %q", got)
	}
	if row := jevAdvisoryRow(t, wsID, id); row != nil {
		t.Fatalf("stale advisory report was persisted: %s", advStr(row))
	}
	var auditJSON string
	dbfx.QueryRow(t, `SELECT audit_log::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&auditJSON)
	var entries []map[string]any
	if err := json.Unmarshal([]byte(auditJSON), &entries); err != nil {
		t.Fatalf("decode proposal audit: %v", err)
	}
	for _, entry := range entries {
		if entry["action"] == "submitted" {
			t.Fatal("stale advisory submission appended a submitted audit entry")
		}
	}
}
