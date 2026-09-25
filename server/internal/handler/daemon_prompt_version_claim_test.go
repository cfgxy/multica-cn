package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// insertPromptVersionRow inserts a minimal prompt_version row for scope/scopeID
// at the given version, so a claim can resolve a real "latest version" instead
// of finding no history at all.
func insertPromptVersionRow(t *testing.T, ctx context.Context, scope, scopeID string, version int) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source)
		VALUES ($1, $2, $3, $4, 'x', 'deadbeef', 'import')
	`, testWorkspaceID, scope, scopeID, version); err != nil {
		t.Fatalf("insert prompt_version(%s, %s, v%d): %v", scope, scopeID, version, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope = $1 AND scope_id = $2`, scope, scopeID)
	})
}

// claimPromptVersionsForTest claims the next queued task for runtimeID and
// returns the persisted agent_task_queue.prompt_versions for that task.
func claimPromptVersionsForTest(t *testing.T, ctx context.Context, runtimeID string) map[string]int32 {
	t.Helper()

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil,
		testWorkspaceID, "prompt-version-claim")
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var claimResp struct {
		Task *struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claimResp.Task == nil {
		t.Fatalf("no task claimed: %s", w.Body.String())
	}

	var raw []byte
	if err := testPool.QueryRow(ctx,
		`SELECT prompt_versions FROM agent_task_queue WHERE id = $1`, claimResp.Task.ID,
	).Scan(&raw); err != nil {
		t.Fatalf("reload claimed task prompt_versions: %v", err)
	}
	out := map[string]int32{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode prompt_versions: %v (raw=%s)", err, raw)
	}
	return out
}

// TestClaim_PromptVersionAttribution_OnlyInjectedTiersGetKeys is the core RUYI-183
// T2 contract: a leader task claim that injects workspace context, project
// instructions, squad instructions and agent instructions must record the
// CURRENT prompt_version.version for each of those four tiers, keyed by scope,
// on agent_task_queue.prompt_versions — nothing more, nothing less.
func TestClaim_PromptVersionAttribution_OnlyInjectedTiersGetKeys(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "pv-attrib runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "pv-attrib leader")
	if _, err := testPool.Exec(ctx, `UPDATE agent SET instructions = 'agent body' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("set agent instructions: %v", err)
	}

	projectID := dbfx.Project(t, "pv-attrib project", testutil.Cols{"instructions": "project body"})
	squadID := dbfx.Squad(t, "pv-attrib squad", agentID, testutil.Cols{"instructions": "squad body"})

	if _, err := testPool.Exec(ctx,
		`UPDATE issue SET project_id = $2, assignee_type = 'squad', assignee_id = $3 WHERE id = $1`,
		issueID, projectID, squadID); err != nil {
		t.Fatalf("bind issue to project/squad: %v", err)
	}

	var origContext []byte
	if err := testPool.QueryRow(ctx, `SELECT context FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&origContext); err != nil {
		t.Fatalf("read original workspace context: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET context = 'workspace body' WHERE id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("set workspace context: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE workspace SET context = $2 WHERE id = $1`, testWorkspaceID, origContext)
	})

	insertPromptVersionRow(t, ctx, "workspace", testWorkspaceID, 3)
	insertPromptVersionRow(t, ctx, "project", projectID, 2)
	insertPromptVersionRow(t, ctx, "squad", squadID, 5)
	insertPromptVersionRow(t, ctx, "agent", agentID, 4)

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, is_leader_task, squad_id)
		VALUES ($1, $2, $3, 'queued', 0, true, $4)
		RETURNING id
	`, agentID, runtimeID, issueID, squadID).Scan(&taskID); err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	got := claimPromptVersionsForTest(t, ctx, runtimeID)
	want := map[string]int32{"workspace": 3, "project": 2, "squad": 5, "agent": 4}
	if len(got) != len(want) {
		t.Fatalf("prompt_versions = %v, want %v", got, want)
	}
	for scope, wantVersion := range want {
		if got[scope] != wantVersion {
			t.Errorf("prompt_versions[%s] = %d, want %d (full=%v)", scope, got[scope], wantVersion, got)
		}
	}
}

// TestClaim_PromptVersionAttribution_SkipsTiersWithNoHistoryOrEmptyContent
// covers the two ways a tier must be OMITTED rather than recorded as version
// 0: no prompt_version row exists yet for it (never saved through governance),
// and the business column is empty so nothing was actually injected. Neither
// case may leave a "0" key on the claimed task, and a claim with no
// attributable tier at all must leave prompt_versions as the default '{}'.
func TestClaim_PromptVersionAttribution_SkipsTiersWithNoHistoryOrEmptyContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "pv-skip runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "pv-skip agent")
	// Empty agent instructions: nothing injected for the agent tier, even
	// though the task always resolves an agent row.
	if _, err := testPool.Exec(ctx, `UPDATE agent SET instructions = '' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("clear agent instructions: %v", err)
	}

	// Project has non-empty instructions but no prompt_version row yet
	// (created before this task ever went through the governance endpoints).
	projectID := dbfx.Project(t, "pv-skip project", testutil.Cols{"instructions": "project body, no history"})
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id = $2 WHERE id = $1`, issueID, projectID); err != nil {
		t.Fatalf("bind issue to project: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("enqueue task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	got := claimPromptVersionsForTest(t, ctx, runtimeID)
	if len(got) != 0 {
		t.Fatalf("expected no attributable tier (empty agent instructions, no project history, no squad, workspace context untouched), got %v", got)
	}
}
