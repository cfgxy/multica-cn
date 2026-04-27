package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListWorkspaceLiveTasks covers the agent presence cache endpoint:
// it must return active (queued/dispatched/running) tasks plus failed tasks
// within the last 2 minutes, and exclude older failed tasks. The 2-minute
// window powers the front-end's auto-clearing "Failed" agent state.
func TestListWorkspaceLiveTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "list-live-tasks-agent", []byte(`{}`))

	// Insert a mix of tasks covering all the cases the handler must classify.
	// completed_at is set explicitly so the time-window filter is testable.
	type taskFixture struct {
		status      string
		completedAt string // SQL expression; "" for NULL
	}
	fixtures := []taskFixture{
		{"queued", ""},
		{"dispatched", ""},
		{"running", ""},
		{"failed", "now() - interval '30 seconds'"},  // recent — should be returned
		{"failed", "now() - interval '5 minutes'"},   // stale — should NOT be returned
		{"completed", "now() - interval '10 seconds'"}, // never returned
		{"cancelled", "now() - interval '10 seconds'"}, // never returned
	}

	insertedIDs := make([]string, 0, len(fixtures))
	for _, f := range fixtures {
		var id string
		var query string
		if f.completedAt == "" {
			query = `INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority)
			         VALUES ($1, $2, $3, 0) RETURNING id`
		} else {
			query = `INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, completed_at)
			         VALUES ($1, $2, $3, 0, ` + f.completedAt + `) RETURNING id`
		}
		if err := testPool.QueryRow(ctx, query, agentID, testRuntimeID, f.status).Scan(&id); err != nil {
			t.Fatalf("failed to insert %s task: %v", f.status, err)
		}
		insertedIDs = append(insertedIDs, id)
	}
	t.Cleanup(func() {
		for _, id := range insertedIDs {
			testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, id)
		}
	})

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/active-tasks", nil)
	testHandler.ListWorkspaceLiveTasks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListWorkspaceLiveTasks: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var tasks []AgentTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	statusCounts := map[string]int{}
	for _, task := range tasks {
		// Only count tasks for our test agent — other tests in this package
		// share the workspace and may have leftover live tasks.
		if task.AgentID != agentID {
			continue
		}
		statusCounts[task.Status]++
	}

	want := map[string]int{
		"queued":     1,
		"dispatched": 1,
		"running":    1,
		"failed":     1, // only the recent one; the 5-minute-old one filtered out
	}
	for status, expected := range want {
		if got := statusCounts[status]; got != expected {
			t.Errorf("status=%s: expected %d task(s) for our agent, got %d", status, expected, got)
		}
	}

	// Completed and cancelled tasks must never appear in the live-tasks endpoint
	// — agent presence derivation only cares about active and recently-failed.
	if statusCounts["completed"] != 0 {
		t.Errorf("completed tasks must not appear in live tasks, got %d", statusCounts["completed"])
	}
	if statusCounts["cancelled"] != 0 {
		t.Errorf("cancelled tasks must not appear in live tasks, got %d", statusCounts["cancelled"])
	}
}

func TestCreateAgent_RejectsDuplicateName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Clean up any agents created by this test.
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE workspace_id = $1 AND name = $2`,
			testWorkspaceID, "duplicate-name-test-agent",
		)
	})

	body := map[string]any{
		"name":                 "duplicate-name-test-agent",
		"description":          "first description",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}

	// First call — creates the agent.
	w1 := httptest.NewRecorder()
	testHandler.CreateAgent(w1, newRequest(http.MethodPost, "/api/agents", body))
	if w1.Code != http.StatusCreated {
		t.Fatalf("first CreateAgent: expected 201, got %d: %s", w1.Code, w1.Body.String())
	}
	var resp1 map[string]any
	if err := json.NewDecoder(w1.Body).Decode(&resp1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	agentID1, _ := resp1["id"].(string)
	if agentID1 == "" {
		t.Fatalf("first CreateAgent: no id in response: %v", resp1)
	}

	// Second call — same name must be rejected with 409 Conflict.
	// The unique constraint prevents silent duplicates; the UI shows a clear error.
	body["description"] = "updated description"
	w2 := httptest.NewRecorder()
	testHandler.CreateAgent(w2, newRequest(http.MethodPost, "/api/agents", body))
	if w2.Code != http.StatusConflict {
		t.Fatalf("second CreateAgent with duplicate name: expected 409, got %d: %s", w2.Code, w2.Body.String())
	}
}
