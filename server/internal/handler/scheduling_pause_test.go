package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// RUYI-608 permission contract, exercised at the HTTP handler boundary.
// The Owner ruling on the issue: ONLY a workspace owner/admin may pause or
// resume scheduling. Everything else is a fail-closed rejection:
//   - a plain member (with or without agent ownership) → 403
//   - an agent-actor request (X-Agent-ID resolution) → 403
//   - any machine credential (X-Actor-Source: task_token/cloud_pat/oauth) → 403
// Reads stay member-visible (the DTO enrichment the UI renders is the same
// state every workspace viewer sees), so GET is asserted for a member too.

// schedulingPauseFixture: a workspace-visible agent owned by a plain member,
// the shape "member created their own agent" produces.
func schedulingPauseFixture(t *testing.T) (agentID, ownerUserID string) {
	t.Helper()
	ownerUserID = createPermissionTestMember(t, "sched-pause-owner@multica.test")
	agentID = createHandlerTestAgent(t, "sched-pause-agent", nil)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET owner_id = $1 WHERE id = $2`, ownerUserID, agentID); err != nil {
		t.Fatalf("assign agent owner: %v", err)
	}
	return agentID, ownerUserID
}

// schedulingCleanup removes the freeze rows a test left on the shared test
// workspace so a failing assertion cannot leak a workspace-wide freeze into
// the next test in the same process.
func schedulingCleanup(t *testing.T, workspaceID, agentID string) {
	t.Helper()
	testPool.Exec(context.Background(),
		`DELETE FROM scheduling_pause WHERE workspace_id = $1 OR agent_id = $2`, workspaceID, agentID)
}

func TestPauseAgentScheduling_PermissionMatrix(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, ownerUserID := schedulingPauseFixture(t)
	memberID := createPermissionTestMember(t, "sched-pause-member@multica.test")
	t.Cleanup(func() { schedulingCleanup(t, testWorkspaceID, agentID) })

	pause := func(userID string, actorSource string) *httptest.ResponseRecorder {
		req := withURLParam(newRequestAs(userID, http.MethodPost, "/api/agents/"+agentID+"/scheduling-pause",
			map[string]any{"reason": "drill"}), "id", agentID)
		if actorSource != "" {
			req.Header.Set("X-Actor-Source", actorSource)
		}
		w := httptest.NewRecorder()
		testHandler.PauseAgentScheduling(w, req)
		return w
	}

	t.Run("plain member who owns the agent is rejected", func(t *testing.T) {
		if w := pause(ownerUserID, ""); w.Code != http.StatusForbidden {
			t.Fatalf("agent owner (member role) pause: got %d; want 403 — %s", w.Code, w.Body.String())
		}
	})

	t.Run("plain member without ownership is rejected", func(t *testing.T) {
		if w := pause(memberID, ""); w.Code != http.StatusForbidden {
			t.Fatalf("plain member pause: got %d; want 403 — %s", w.Code, w.Body.String())
		}
	})

	t.Run("machine credential is rejected even for the owner", func(t *testing.T) {
		for _, source := range []string{"task_token", "cloud_pat", "oauth"} {
			if w := pause(testUserID, source); w.Code != http.StatusForbidden {
				t.Fatalf("machine credential %s pause: got %d; want 403 — %s", source, w.Code, w.Body.String())
			}
		}
	})

	t.Run("agent actor is rejected", func(t *testing.T) {
		// resolveActor classifies via X-Agent-ID + X-Task-ID (the legacy CLI
		// flow) — exactly the shape a self-pausing agent would present.
		req := withURLParam(newRequestAs(ownerUserID, http.MethodPost, "/api/agents/"+agentID+"/scheduling-pause",
			map[string]any{"reason": "self pause"}), "id", agentID)
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Task-ID", agentID) // any non-empty task id drives the actor=agent path
		w := httptest.NewRecorder()
		testHandler.PauseAgentScheduling(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("agent-actor pause: got %d; want 403 — %s", w.Code, w.Body.String())
		}
	})

	t.Run("workspace owner pauses successfully", func(t *testing.T) {
		w := pause(testUserID, "")
		if w.Code != http.StatusOK {
			t.Fatalf("workspace owner pause: got %d; want 200 — %s", w.Code, w.Body.String())
		}
		var resp schedulingPauseResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode pause response: %v", err)
		}
		if !resp.Paused || resp.Scope != "agent" {
			t.Fatalf("pause response = %+v; want paused with agent scope", resp)
		}
	})
}

func TestResumeAgentScheduling_IdempotentAndPermissioned(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, _ := schedulingPauseFixture(t)
	memberID := createPermissionTestMember(t, "sched-resume-member@multica.test")
	t.Cleanup(func() { schedulingCleanup(t, testWorkspaceID, agentID) })

	resume := func(userID string) *httptest.ResponseRecorder {
		req := withURLParam(newRequestAs(userID, http.MethodDelete,
			"/api/agents/"+agentID+"/scheduling-pause", nil), "id", agentID)
		w := httptest.NewRecorder()
		testHandler.ResumeAgentScheduling(w, req)
		return w
	}

	// Member resume is rejected before anything else.
	if w := resume(memberID); w.Code != http.StatusForbidden {
		t.Fatalf("member resume: got %d; want 403 — %s", w.Code, w.Body.String())
	}

	// Resume with NO freeze row is a no-op success reporting the queue depth.
	w := resume(testUserID)
	if w.Code != http.StatusOK {
		t.Fatalf("no-op resume: got %d; want 200 — %s", w.Code, w.Body.String())
	}
	var resp schedulingPauseResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resume response: %v", err)
	}
	if resp.Paused {
		t.Fatalf("no-op resume answered paused=true — resume must read as resumed even without a row")
	}

	// Pause then resume clears the row.
	req := withURLParam(newRequestAs(testUserID, http.MethodPost, "/api/agents/"+agentID+"/scheduling-pause",
		map[string]any{"reason": "resume drill"}), "id", agentID)
	w = httptest.NewRecorder()
	testHandler.PauseAgentScheduling(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pause setup: got %d — %s", w.Code, w.Body.String())
	}
	w = resume(testUserID)
	if w.Code != http.StatusOK {
		t.Fatalf("resume after pause: got %d — %s", w.Code, w.Body.String())
	}
	var rows int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduling_pause WHERE agent_id = $1`, agentID).Scan(&rows); err != nil {
		t.Fatalf("count freeze rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("scheduling_pause rows = %d after resume; want 0", rows)
	}
}

func TestWorkspaceSchedulingPause_PermissionAndShape(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	memberID := createPermissionTestMember(t, "sched-ws-member@multica.test")
	t.Cleanup(func() { schedulingCleanup(t, testWorkspaceID, "") })

	pause := func(userID string) *httptest.ResponseRecorder {
		req := withURLParam(newRequestAs(userID, http.MethodPost,
			"/api/workspaces/"+testWorkspaceID+"/scheduling-pause",
			map[string]any{"reason": "ws drill"}), "id", testWorkspaceID)
		w := httptest.NewRecorder()
		testHandler.PauseWorkspaceScheduling(w, req)
		return w
	}

	if w := pause(memberID); w.Code != http.StatusForbidden {
		t.Fatalf("member workspace pause: got %d; want 403 — %s", w.Code, w.Body.String())
	}

	w := pause(testUserID)
	if w.Code != http.StatusOK {
		t.Fatalf("owner workspace pause: got %d; want 200 — %s", w.Code, w.Body.String())
	}
	var resp schedulingPauseResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode pause response: %v", err)
	}
	if !resp.Paused || resp.Scope != "workspace" {
		t.Fatalf("workspace pause response = %+v; want paused with workspace scope", resp)
	}

	// Member READ stays allowed (fail-open reads, fail-closed writes).
	req := withURLParam(newRequestAs(memberID, http.MethodGet,
		"/api/workspaces/"+testWorkspaceID+"/scheduling-pause", nil), "id", testWorkspaceID)
	w = httptest.NewRecorder()
	testHandler.GetWorkspaceSchedulingPause(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("member workspace GET: got %d; want 200 — %s", w.Code, w.Body.String())
	}

	// Resume clears the workspace freeze.
	req = withURLParam(newRequestAs(testUserID, http.MethodDelete,
		"/api/workspaces/"+testWorkspaceID+"/scheduling-pause", nil), "id", testWorkspaceID)
	w = httptest.NewRecorder()
	testHandler.ResumeWorkspaceScheduling(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("workspace resume: got %d — %s", w.Code, w.Body.String())
	}
}

// ---- List/detail projection parity (RUYI-608 QA P1 rework) ----
//
// The QA pass on 7d980dbd found the freeze fields inconsistent across the
// projection surface: the workspaces LIST answered paused=false/0 while the
// detail endpoint answered true/2, the agent DETAIL dropped the queued
// count the list carried, and the tasks page had nothing to render. These
// tests pin the repaired contract: for each resource, list and detail must
// answer the same scheduling fields.

// insertQueuedTask seeds one queued agent_task_queue row for agentID and
// registers its cleanup.
func insertQueuedTask(t *testing.T, agentID string) {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority)
		 VALUES ($1, $2, 'queued', 0) RETURNING id`, agentID, testRuntimeID).Scan(&id); err != nil {
		t.Fatalf("insert queued task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, id)
	})
}

func TestWorkspaceList_SchedulingProjectionParity(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "sched-wslist-agent", nil)
	t.Cleanup(func() { schedulingCleanup(t, testWorkspaceID, agentID) })
	insertQueuedTask(t, agentID)

	readList := func() WorkspaceResponse {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.ListWorkspaces(w, newRequestAs(testUserID, http.MethodGet, "/api/workspaces", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("ListWorkspaces: got %d — %s", w.Code, w.Body.String())
		}
		var list []WorkspaceResponse
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		for _, ws := range list {
			if ws.ID == testWorkspaceID {
				return ws
			}
		}
		t.Fatalf("workspace %s missing from list", testWorkspaceID)
		return WorkspaceResponse{}
	}
	readDetail := func() WorkspaceResponse {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.GetWorkspace(w, withURLParam(newRequestAs(testUserID, http.MethodGet,
			"/api/workspaces/"+testWorkspaceID, nil), "id", testWorkspaceID))
		if w.Code != http.StatusOK {
			t.Fatalf("GetWorkspace: got %d — %s", w.Code, w.Body.String())
		}
		var resp WorkspaceResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		return resp
	}

	// Unfrozen baseline: not paused; the count is zero because the detail
	// endpoint scopes it to the freeze (ErrNoRows → zero value), and the
	// list must mirror that shape.
	before := readList()
	if before.SchedulingPaused || before.SchedulingQueuedCount != 0 {
		t.Fatalf("unfrozen list = paused=%v count=%d; want false/0",
			before.SchedulingPaused, before.SchedulingQueuedCount)
	}

	// Freeze at the workspace level; the frozen queue depth is now 1.
	pauseReq := withURLParam(newRequestAs(testUserID, http.MethodPost,
		"/api/workspaces/"+testWorkspaceID+"/scheduling-pause",
		map[string]any{"reason": "list projection drill"}), "id", testWorkspaceID)
	wPause := httptest.NewRecorder()
	testHandler.PauseWorkspaceScheduling(wPause, pauseReq)
	if wPause.Code != http.StatusOK {
		t.Fatalf("pause setup: got %d — %s", wPause.Code, wPause.Body.String())
	}

	list := readList()
	detail := readDetail()
	if !list.SchedulingPaused {
		t.Fatalf("frozen list answered paused=false — list projection missing (QA P1)")
	}
	if list.SchedulingPausedReason != "list projection drill" {
		t.Fatalf("list reason = %q; want %q", list.SchedulingPausedReason, "list projection drill")
	}
	if list.SchedulingQueuedCount != 1 {
		t.Fatalf("list queued_count = %d; want 1", list.SchedulingQueuedCount)
	}
	if list.SchedulingPaused != detail.SchedulingPaused ||
		list.SchedulingQueuedCount != detail.SchedulingQueuedCount {
		t.Fatalf("list/detail mismatch: list paused=%v count=%d, detail paused=%v count=%d",
			list.SchedulingPaused, list.SchedulingQueuedCount,
			detail.SchedulingPaused, detail.SchedulingQueuedCount)
	}

	// Resume restores the unfrozen shape.
	resumeReq := withURLParam(newRequestAs(testUserID, http.MethodDelete,
		"/api/workspaces/"+testWorkspaceID+"/scheduling-pause", nil), "id", testWorkspaceID)
	wResume := httptest.NewRecorder()
	testHandler.ResumeWorkspaceScheduling(wResume, resumeReq)
	if wResume.Code != http.StatusOK {
		t.Fatalf("resume: got %d — %s", wResume.Code, wResume.Body.String())
	}
	if after := readList(); after.SchedulingPaused {
		t.Fatalf("resumed list answered paused=true")
	}
}

func TestAgentDetail_SchedulingQueuedCountParity(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "sched-detail-agent", nil)
	t.Cleanup(func() { schedulingCleanup(t, testWorkspaceID, agentID) })
	insertQueuedTask(t, agentID)

	// The list carries the count (batch fill via
	// CountQueuedTasksByWorkspacePerAgent); the detail must answer the same
	// number for the same agent.
	wl := httptest.NewRecorder()
	testHandler.ListAgents(wl, newRequestAs(testUserID, http.MethodGet, "/api/agents", nil))
	if wl.Code != http.StatusOK {
		t.Fatalf("ListAgents: got %d — %s", wl.Code, wl.Body.String())
	}
	var agents []AgentResponse
	if err := json.Unmarshal(wl.Body.Bytes(), &agents); err != nil {
		t.Fatalf("decode agents list: %v", err)
	}
	var fromList *AgentResponse
	for i := range agents {
		if agents[i].ID == agentID {
			fromList = &agents[i]
			break
		}
	}
	if fromList == nil {
		t.Fatalf("agent %s missing from list", agentID)
	}

	wd := httptest.NewRecorder()
	testHandler.GetAgent(wd, withURLParam(newRequestAs(testUserID, http.MethodGet,
		"/api/agents/"+agentID, nil), "id", agentID))
	if wd.Code != http.StatusOK {
		t.Fatalf("GetAgent: got %d — %s", wd.Code, wd.Body.String())
	}
	var detail AgentResponse
	if err := json.Unmarshal(wd.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode agent detail: %v", err)
	}

	if detail.SchedulingQueuedCount != fromList.SchedulingQueuedCount {
		t.Fatalf("detail queued_count = %d; list says %d — detail projection drops the count (QA P1)",
			detail.SchedulingQueuedCount, fromList.SchedulingQueuedCount)
	}
	if detail.SchedulingQueuedCount != 1 {
		t.Fatalf("detail queued_count = %d; want 1", detail.SchedulingQueuedCount)
	}
}
