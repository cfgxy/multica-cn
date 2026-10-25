package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/legislation"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestEnactRecoveryRejectsStructuredStaticTarget(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)
	withJevAdvisory(t, &legislation.AdvisoryClient{})

	initialID := createDraft(t, wsID, memberID, "恢复修订条款", cleanClauseFor("恢复修订条款"))
	submitDraft(t, wsID, memberID, initialID)
	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	createResponse := testutil.Call(t, http.HandlerFunc(ownerApprove.ServeHTTP),
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+initialID+"/approve",
			map[string]any{"confirm_diff_previewed": true}, "id", initialID))
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create clause: expected 200, got %d: %s", createResponse.Code, createResponse.Text())
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
	enactResponse := testutil.Call(t, http.HandlerFunc(ownerEnact.ServeHTTP),
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+revisionID+"/enact", nil, "id", revisionID))
	if enactResponse.Code != http.StatusConflict {
		t.Fatalf("structured static recovery: expected 409, got %d: %s", enactResponse.Code, enactResponse.Text())
	}
	if got := proposalStatus(t, wsID, revisionID); got != "gate_failed" {
		t.Fatalf("invalid recovery status = %q, want gate_failed", got)
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
	rework := testutil.Call(t, testHandler.ReworkPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+revisionID+"/rework", nil, "id", revisionID))
	if rework.Code != http.StatusOK || proposalStatus(t, wsID, revisionID) != "draft" {
		t.Fatalf("invalid structured row could not be reworked: %d %s", rework.Code, rework.Text())
	}
}

func TestApprovedEnactAndApproveRecoveryUseConsistentLockOrder(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)
	withJevAdvisory(t, &legislation.AdvisoryClient{})

	initialID := createDraft(t, wsID, memberID, "恢复锁序条款", cleanClauseFor("恢复锁序条款"))
	submitDraft(t, wsID, memberID, initialID)
	ownerApprove := ownerRoute(http.HandlerFunc(testHandler.ApprovePromptProposal))
	created := testutil.Call(t, http.HandlerFunc(ownerApprove.ServeHTTP),
		legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+initialID+"/approve",
			map[string]any{"confirm_diff_previewed": true}, "id", initialID))
	if created.Code != http.StatusOK {
		t.Fatalf("create clause: expected 200, got %d: %s", created.Code, created.Text())
	}
	var clauseID string
	dbfx.QueryRow(t, `SELECT clause_id::text FROM prompt_proposal WHERE workspace_id = $1 AND id = $2`, wsID, initialID).Scan(&clauseID)

	revisionID := createDraft(t, wsID, memberID, "恢复锁序条款", cleanClauseFor("恢复锁序条款"))
	dbfx.Exec(t, `UPDATE prompt_proposal SET change_kind = 'revise_clause', clause_id = $3
		WHERE workspace_id = $1 AND id = $2`, wsID, revisionID, clauseID)
	submitDraft(t, wsID, memberID, revisionID)
	dbfx.Exec(t, `UPDATE prompt_proposal SET status = 'approved', enactment_target = 'clause_library'
		WHERE workspace_id = $1 AND id = $2`, wsID, revisionID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock barrier: %v", err)
	}
	defer blocker.Rollback(context.Background())
	var blockerPID int32
	if err := blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatalf("read lock barrier pid: %v", err)
	}
	if err := lockClauseReferenceWorkspaceTx(ctx, testHandler.Queries.WithTx(blocker), parseUUID(wsID)); err != nil {
		t.Fatalf("hold workspace clause lock: %v", err)
	}

	type response struct {
		res *testutil.Response
	}
	results := make(chan response, 2)
	ownerEnact := ownerRoute(http.HandlerFunc(testHandler.EnactPromptProposal))
	go func() {
		results <- response{testutil.Call(t, http.HandlerFunc(ownerEnact.ServeHTTP),
			legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+revisionID+"/enact", nil, "id", revisionID))}
	}()
	waitForBlockedDatabaseQuery(t, ctx, "pg_advisory_xact_lock", blockerPID)
	go func() {
		results <- response{testutil.Call(t, http.HandlerFunc(ownerApprove.ServeHTTP),
			legislationReq(ownerID, wsID, http.MethodPost, "/api/prompt-legislation/proposals/"+revisionID+"/approve",
				map[string]any{"confirm_diff_previewed": true}, "id", revisionID))}
	}()
	if err := waitForClauseReferenceLockWaiters(wsID, 2); err != nil {
		t.Fatalf("both recovery requests did not wait on the workspace lock: %v", err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release lock barrier: %v", err)
	}

	seen := map[int]int{}
	for range 2 {
		select {
		case got := <-results:
			seen[got.res.Code]++
			if got.res.Code != http.StatusOK && got.res.Code != http.StatusConflict {
				t.Fatalf("recovery returned %d: %s", got.res.Code, got.res.Text())
			}
		case <-ctx.Done():
			t.Fatalf("recovery requests deadlocked or timed out: %v", ctx.Err())
		}
	}
	if seen[http.StatusOK] != 1 || seen[http.StatusConflict] != 1 || proposalStatus(t, wsID, revisionID) != "enacted" {
		t.Fatalf("recovery did not enact exactly once; statuses=%v proposal=%s", seen, proposalStatus(t, wsID, revisionID))
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

	req := legislationReq(memberID, wsID, http.MethodPost,
		"/api/prompt-legislation/proposals/"+revisionID+"/submit", nil, "id", revisionID)
	ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
	defer cancel()
	blocker, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin enact transaction: %v", err)
	}
	defer blocker.Rollback(context.Background())
	if err := lockClauseReferenceWorkspaceTx(ctx, testHandler.Queries.WithTx(blocker), parseUUID(wsID)); err != nil {
		t.Fatalf("lock clause references: %v", err)
	}

	type response struct {
		code int
		body string
	}
	result := make(chan response, 1)
	req = req.WithContext(ctx)
	go func() {
		res := testutil.Call(t, testHandler.SubmitPromptProposal, req)
		result <- response{code: res.Code, body: res.Text()}
	}()
	if err := waitForClauseReferenceLockWaiters(wsID, 1); err != nil {
		select {
		case got := <-result:
			t.Fatalf("submit returned without waiting on the workspace lock: %d %s; %v", got.code, got.body, err)
		default:
			t.Fatalf("submit did not wait on the workspace lock: %v", err)
		}
	}

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
		res := testutil.Call(t, testHandler.SubmitPromptProposal, req)
		result <- response{code: res.Code, body: res.Text()}
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

func TestSubmitNonDraftDoesNotCallAdvisory(t *testing.T) {
	wsID, _, memberID := legislationFixture(t)
	id := createDraft(t, wsID, memberID, "重复提交草案", cleanClauseFor("重复提交草案"))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task":"kev_local","p_success":0.8,"p_failure":0.2,"threshold":0.3,"warn":false,"decision":"pass","engine":"test","threshold_source":"default"}`))
	}))
	defer server.Close()
	withJevAdvisory(t, &legislation.AdvisoryClient{BaseURL: server.URL, Timeout: 10 * time.Second})
	path := "/api/prompt-legislation/proposals/" + id + "/submit"
	first := testutil.Call(t, testHandler.SubmitPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, path, nil, "id", id))
	if first.Code != http.StatusOK {
		t.Fatalf("first submit: expected 200, got %d: %s", first.Code, first.Text())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("first submit advisory calls = %d, want 1", got)
	}
	second := testutil.Call(t, testHandler.SubmitPromptProposal,
		legislationReq(memberID, wsID, http.MethodPost, path, nil, "id", id))
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate submit: expected 409, got %d: %s", second.Code, second.Text())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("non-draft retry called advisory; total calls = %d, want 1", got)
	}
}
