package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func opinionRoute(h http.HandlerFunc, role string) http.Handler {
	if role == "member" {
		return middleware.RequireWorkspaceMember(testHandler.Queries)(h)
	}
	return middleware.RequireWorkspaceRole(testHandler.Queries, "owner", "admin")(h)
}

func opinionCall(t *testing.T, router http.Handler, userID, wsID, method, path string, urlParams map[string]string) (int, string) {
	t.Helper()
	req := legislationReq(userID, wsID, method, path, nil)
	rctx := chi.NewRouteContext()
	for k, v := range urlParams {
		rctx.URLParams.Add(k, v)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func setupTestOpinion(t *testing.T, dirID, entryKey, action, proposedContent string) (string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	entry := knowledgeEntryByKey(t, dirID, entryKey)
	var runID, findingID, opinionID string
	err := testPool.QueryRow(ctx, `
INSERT INTO knowledge_governance_run (workspace_id, dir_id, agent_id, status, title, prompt_version, trigger, created_by)
VALUES ($1, $2, $3, 'succeeded', 'Test Governance Run', 'v1.0', 'manual', $3) RETURNING id::text`, testWorkspaceID, dirID, testUserID).Scan(&runID)
	if err != nil {
		t.Fatalf("insert governance run: %v", err)
	}
	err = testPool.QueryRow(ctx, `
INSERT INTO knowledge_governance_finding (workspace_id, run_id, entry_id, entry_key, original_content, content_version, content_sha256, scope, verdict, applicable_scope, recommendation)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'global', 'revised', 'all', 'update content') RETURNING id::text`, testWorkspaceID, runID, entry.ID, entry.Key, entry.Content, entry.CurrentVersion, entry.ContentSha256).Scan(&findingID)
	if err != nil {
		t.Fatalf("insert governance finding: %v", err)
	}
	err = testPool.QueryRow(ctx, `
INSERT INTO knowledge_governance_opinion (workspace_id, dir_id, entry_id, run_id, finding_id, action, proposed_content, target_version, target_content_sha256, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending') RETURNING id::text`, testWorkspaceID, dirID, entry.ID, runID, findingID, action, proposedContent, entry.CurrentVersion, entry.ContentSha256).Scan(&opinionID)
	if err != nil {
		t.Fatalf("insert governance opinion: %v", err)
	}
	return entry.ID, runID, findingID, opinionID
}

func TestKnowledgeGovernanceOpinionReject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	const key = "opinion-reject-test"
	postKnowledgeResults(t, "test-daemon", map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{key: "original content"},
		}},
	})
	_, _, _, opinionID := setupTestOpinion(t, candidate, key, "revise", "new proposed content")

	router := opinionRoute(testHandler.DecideKnowledgeGovernanceOpinion, "owner")
	code, body := opinionCall(t, router, testUserID, testWorkspaceID, http.MethodPost, "/api/knowledge/governance/opinions/"+opinionID+"/reject", map[string]string{
		"id": opinionID, "decision": "reject",
	})
	if code != http.StatusOK {
		t.Fatalf("reject opinion: expected 200, got %d: %s", code, body)
	}
	var status string
	testPool.QueryRow(context.Background(), `SELECT status FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&status)
	if status != "rejected" {
		t.Fatalf("opinion status = %q, want rejected", status)
	}
}

func TestKnowledgeGovernanceOpinionCASDrift(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	const key = "opinion-drift-test"
	postKnowledgeResults(t, "test-daemon", map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{key: "original content"},
		}},
	})
	entryID, _, _, opinionID := setupTestOpinion(t, candidate, key, "revise", "drift proposed content")

	// Simulate drift by changing the entry's content and version
	_, err := testPool.Exec(context.Background(), `
UPDATE knowledge_entry SET content = 'drifted content', content_sha256 = 'driftedsha', current_version = 2 WHERE id = $1`, entryID)
	if err != nil {
		t.Fatalf("update entry for drift: %v", err)
	}

	router := opinionRoute(testHandler.DecideKnowledgeGovernanceOpinion, "owner")
	code, body := opinionCall(t, router, testUserID, testWorkspaceID, http.MethodPost, "/api/knowledge/governance/opinions/"+opinionID+"/approve", map[string]string{
		"id": opinionID, "decision": "approve",
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 conflict on drift, got %d: %s", code, body)
	}
	var status string
	testPool.QueryRow(context.Background(), `SELECT status FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&status)
	if status != "stale" {
		t.Fatalf("opinion status = %q, want stale", status)
	}
}

func TestKnowledgeGovernanceOpinionUnmanagedRevision(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	const key = "opinion-unmanaged-revision"
	postKnowledgeResults(t, "test-daemon", map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{key: "unmanaged original content"},
		}},
	})
	entryID, _, _, opinionID := setupTestOpinion(t, candidate, key, "revise", "unmanaged revised content")

	router := opinionRoute(testHandler.DecideKnowledgeGovernanceOpinion, "owner")
	code, body := opinionCall(t, router, testUserID, testWorkspaceID, http.MethodPost, "/api/knowledge/governance/opinions/"+opinionID+"/approve", map[string]string{
		"id": opinionID, "decision": "approve",
	})
	if code != http.StatusOK {
		t.Fatalf("approve unmanaged revision: expected 200, got %d: %s", code, body)
	}
	var status string
	testPool.QueryRow(context.Background(), `SELECT status FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&status)
	if status != "applied" {
		t.Fatalf("opinion status = %q, want applied", status)
	}

	var entryContent, versionSource string
	var currentVer int
	testPool.QueryRow(context.Background(), `SELECT content, current_version FROM knowledge_entry WHERE id = $1`, entryID).Scan(&entryContent, &currentVer)
	if entryContent != "unmanaged revised content" || currentVer != 2 {
		t.Fatalf("entry content=%q version=%d, want revised content and version 2", entryContent, currentVer)
	}
	testPool.QueryRow(context.Background(), `SELECT source FROM knowledge_entry_version WHERE entry_id = $1 AND version = 2`, entryID).Scan(&versionSource)
	if versionSource != "governance" {
		t.Fatalf("version source = %q, want governance", versionSource)
	}
}

func TestKnowledgeGovernanceOpinionManagedRevisionAndDaemonWriteback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-opinion-managed-revision"
	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	const key = "opinion-managed-revision"
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{key: "initial memory content"},
		}},
	})
	testutil.Call(t, testHandler.PutKnowledgeDirManaged,
		withURLParam(newRequest(http.MethodPut, "/api/knowledge/dirs/"+candidate+"/managed",
			map[string]any{"managed_active": true}), "id", candidate)).Want(http.StatusOK)

	entryID, _, _, opinionID := setupTestOpinion(t, candidate, key, "revise", "managed approved revision content")

	// Owner approves the revision
	router := opinionRoute(testHandler.DecideKnowledgeGovernanceOpinion, "owner")
	code, body := opinionCall(t, router, testUserID, testWorkspaceID, http.MethodPost, "/api/knowledge/governance/opinions/"+opinionID+"/approve", map[string]string{
		"id": opinionID, "decision": "approve",
	})
	if code != http.StatusOK {
		t.Fatalf("approve managed revision: expected 200, got %d: %s", code, body)
	}

	// Should be in 'applying' state, entry writeback_state queued
	var opinionStatus string
	var lifecycleEventID string
	testPool.QueryRow(context.Background(), `SELECT status, lifecycle_event_id::text FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&opinionStatus, &lifecycleEventID)
	if opinionStatus != "applying" {
		t.Fatalf("opinion status = %q, want applying", opinionStatus)
	}

	// Daemon pulls plan and claims the write-back job
	plan := knowledgePlan(t, daemonID)
	if len(plan.Workspaces) != 1 || len(plan.Workspaces[0].Writebacks) != 1 {
		t.Fatalf("expected 1 writeback job in daemon plan, got %+v", plan)
	}
	wbJob := plan.Workspaces[0].Writebacks[0]
	if wbJob.Operation != "restore" || wbJob.Content != "managed approved revision content" {
		t.Fatalf("writeback job = %+v, want restore with revision content", wbJob)
	}

	// Daemon reports successful writeback
	postKnowledgeResults(t, daemonID, map[string]any{
		"writebacks": []map[string]any{{
			"event_id":    wbJob.EventID,
			"entry_id":    wbJob.EntryID,
			"claim_token": wbJob.ClaimToken,
			"ok":          true,
		}},
	})

	// Check final state
	testPool.QueryRow(context.Background(), `SELECT status FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&opinionStatus)
	if opinionStatus != "applied" {
		t.Fatalf("opinion status = %q, want applied after writeback", opinionStatus)
	}

	var entryContent string
	var currentVer int
	testPool.QueryRow(context.Background(), `SELECT content, current_version FROM knowledge_entry WHERE id = $1`, entryID).Scan(&entryContent, &currentVer)
	if entryContent != "managed approved revision content" || currentVer != 2 {
		t.Fatalf("entry content=%q version=%d, want updated content and version 2", entryContent, currentVer)
	}
}

func TestKnowledgeGovernanceOpinionManagedDeletionAndDaemonWriteback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-opinion-managed-deletion"
	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	const key = "opinion-managed-deletion"
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{key: "to be deleted"},
		}},
	})
	testutil.Call(t, testHandler.PutKnowledgeDirManaged,
		withURLParam(newRequest(http.MethodPut, "/api/knowledge/dirs/"+candidate+"/managed",
			map[string]any{"managed_active": true}), "id", candidate)).Want(http.StatusOK)

	entryID, _, _, opinionID := setupTestOpinion(t, candidate, key, "delete", "")

	// Owner approves deletion
	router := opinionRoute(testHandler.DecideKnowledgeGovernanceOpinion, "owner")
	code, body := opinionCall(t, router, testUserID, testWorkspaceID, http.MethodPost, "/api/knowledge/governance/opinions/"+opinionID+"/approve", map[string]string{
		"id": opinionID, "decision": "approve",
	})
	if code != http.StatusOK {
		t.Fatalf("approve managed deletion: expected 200, got %d: %s", code, body)
	}

	var opinionStatus string
	testPool.QueryRow(context.Background(), `SELECT status FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&opinionStatus)
	if opinionStatus != "applying" {
		t.Fatalf("opinion status = %q, want applying", opinionStatus)
	}

	// Daemon pulls plan
	plan := knowledgePlan(t, daemonID)
	if len(plan.Workspaces) != 1 || len(plan.Workspaces[0].Writebacks) != 1 {
		t.Fatalf("expected 1 writeback job in daemon plan, got %+v", plan)
	}
	wbJob := plan.Workspaces[0].Writebacks[0]
	if wbJob.Operation != "remove" {
		t.Fatalf("writeback job operation = %q, want remove", wbJob.Operation)
	}

	// Daemon reports success
	postKnowledgeResults(t, daemonID, map[string]any{
		"writebacks": []map[string]any{{
			"event_id":    wbJob.EventID,
			"entry_id":    wbJob.EntryID,
			"claim_token": wbJob.ClaimToken,
			"ok":          true,
		}},
	})

	testPool.QueryRow(context.Background(), `SELECT status FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&opinionStatus)
	if opinionStatus != "applied" {
		t.Fatalf("opinion status = %q, want applied", opinionStatus)
	}

	var mirrorState string
	testPool.QueryRow(context.Background(), `SELECT mirror_state FROM knowledge_entry WHERE id = $1`, entryID).Scan(&mirrorState)
	if mirrorState != "source_deleted" {
		t.Fatalf("entry mirror_state = %q, want source_deleted", mirrorState)
	}
}

func TestKnowledgeGovernanceOpinionDaemonWritebackFailure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	const daemonID = "daemon-opinion-writeback-fail"
	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	const key = "opinion-writeback-fail"
	postKnowledgeResults(t, daemonID, map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{key: "content"},
		}},
	})
	testutil.Call(t, testHandler.PutKnowledgeDirManaged,
		withURLParam(newRequest(http.MethodPut, "/api/knowledge/dirs/"+candidate+"/managed",
			map[string]any{"managed_active": true}), "id", candidate)).Want(http.StatusOK)

	_, _, _, opinionID := setupTestOpinion(t, candidate, key, "revise", "content revision")

	router := opinionRoute(testHandler.DecideKnowledgeGovernanceOpinion, "owner")
	opinionCall(t, router, testUserID, testWorkspaceID, http.MethodPost, "/api/knowledge/governance/opinions/"+opinionID+"/approve", map[string]string{
		"id": opinionID, "decision": "approve",
	})

	plan := knowledgePlan(t, daemonID)
	wbJob := plan.Workspaces[0].Writebacks[0]

	// Daemon reports failure
	postKnowledgeResults(t, daemonID, map[string]any{
		"writebacks": []map[string]any{{
			"event_id":    wbJob.EventID,
			"entry_id":    wbJob.EntryID,
			"claim_token": wbJob.ClaimToken,
			"ok":          false,
			"error":       "permission denied on physical file",
		}},
	})

	var opinionStatus, opinionErr string
	testPool.QueryRow(context.Background(), `SELECT status, error FROM knowledge_governance_opinion WHERE id = $1`, opinionID).Scan(&opinionStatus, &opinionErr)
	if opinionStatus != "failed" || opinionErr != "permission denied on physical file" {
		t.Fatalf("opinion status=%q error=%q, want failed with daemon error", opinionStatus, opinionErr)
	}
}

func TestKnowledgeGovernanceOpinionListAPI(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Fatal("database fixture is required")
	}
	candidate := registerKnowledgeDir(t, "candidate_cli", t.TempDir())
	const key = "opinion-list-api"
	postKnowledgeResults(t, "test-daemon", map[string]any{
		"scans": []map[string]any{{
			"dir_id": candidate, "trigger_source": "initial", "ok": true,
			"memories": map[string]string{key: "list content"},
		}},
	})
	_, _, _, opinionID := setupTestOpinion(t, candidate, key, "revise", "proposed list content")

	router := opinionRoute(testHandler.ListKnowledgeGovernanceOpinions, "member")
	code, body := opinionCall(t, router, testUserID, testWorkspaceID, http.MethodGet, "/api/knowledge/governance/opinions", nil)
	if code != http.StatusOK {
		t.Fatalf("list opinions: expected 200, got %d: %s", code, body)
	}
	var res struct {
		Opinions []KnowledgeGovernanceOpinionResponse `json:"opinions"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("unmarshal list opinions: %v", err)
	}
	found := false
	for _, op := range res.Opinions {
		if op.ID == opinionID {
			found = true
			if op.Action != "revise" || op.ProposedContent != "proposed list content" || op.Status != "pending" {
				t.Fatalf("opinion details mismatch: %+v", op)
			}
			break
		}
	}
	if !found {
		t.Fatalf("opinion %s not found in list response", opinionID)
	}
}
