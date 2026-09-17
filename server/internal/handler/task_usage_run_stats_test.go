package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListTasksByIssueHydratesRunStats is the RUYI-154 read-side guard: turns,
// compactions and max_context_tokens are run-scoped (one true value per task),
// but task_usage stores a row per (task, provider, model) — a run that reports
// more than one model must not have its run-level reading summed against
// itself. It also pins context_tokens (RUYI-107) finally reaching the read
// path this task adds.
func TestListTasksByIssueHydratesRunStats(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "RunStatsListAgent", []byte("[]"))

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'run-stats-list-issue', 'todo', 'medium', $2, 'member',
			(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1), 0)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	newTask := func(status string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id)
			VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 0, $3)
			RETURNING id
		`, agentID, status, issueID).Scan(&id); err != nil {
			t.Fatalf("create task: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, id) })
		return id
	}

	// A run that spilled across two models. The larger reading (model B) must
	// win the collapse, not the sum of the two rows.
	multiModelTask := newTask("completed")
	// A run that predates RUYI-154 (and RUYI-107): no run-stat columns at all.
	noStatsTask := newTask("completed")

	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens, turns, compactions, max_context_tokens, context_tokens)
		VALUES
			($1, 'anthropic', 'claude-opus-5', 1000, 100, 0, 0, 12, 1, 90000, 80000),
			($1, 'anthropic', 'claude-haiku-4-5', 200, 20, 0, 0, 12, 3, 150000, 80000)
	`, multiModelTask); err != nil {
		t.Fatalf("insert multi-model task usage: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
		VALUES ($1, 'anthropic', 'claude-opus-5', 500, 50, 0, 0)
	`, noStatsTask); err != nil {
		t.Fatalf("insert no-stats task usage: %v", err)
	}

	req := newRequest("GET", "/api/issues/"+issueID+"/task-runs", nil)
	req = withURLParam(req, "id", issueID)
	w := httptest.NewRecorder()
	testHandler.ListTasksByIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []AgentTaskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode task list: %v", err)
	}
	byID := make(map[string]AgentTaskResponse, len(resp))
	for _, task := range resp {
		byID[task.ID] = task
	}

	multi, ok := byID[multiModelTask]
	if !ok {
		t.Fatalf("multi-model task %s missing from response: %s", multiModelTask, w.Body.String())
	}
	if multi.Turns == nil || *multi.Turns != 12 {
		t.Errorf("turns = %v, want 12 (same on both rows, not doubled)", multi.Turns)
	}
	if multi.Compactions == nil || *multi.Compactions != 3 {
		t.Errorf("compactions = %v, want 3 (max across rows, not sum 1+3=4)", multi.Compactions)
	}
	if multi.MaxContextTokens == nil || *multi.MaxContextTokens != 150000 {
		t.Errorf("max_context_tokens = %v, want 150000 (max across rows, not sum)", multi.MaxContextTokens)
	}
	if multi.ContextTokens == nil || *multi.ContextTokens != 80000 {
		t.Errorf("context_tokens = %v, want 80000", multi.ContextTokens)
	}

	noStats, ok := byID[noStatsTask]
	if !ok {
		t.Fatalf("no-stats task %s missing from response", noStatsTask)
	}
	if noStats.Turns != nil || noStats.Compactions != nil || noStats.MaxContextTokens != nil || noStats.ContextTokens != nil {
		t.Errorf("no-stats task carries a reading, want all nil: turns=%v compactions=%v max_ctx=%v ctx=%v",
			noStats.Turns, noStats.Compactions, noStats.MaxContextTokens, noStats.ContextTokens)
	}
	// omitempty must drop the keys entirely for a run with no reading, the same
	// contract TestListTasksByIssueHydratesUsage pins for `usage`.
	var raw []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw task list: %v", err)
	}
	for _, task := range raw {
		if task["id"] != noStatsTask {
			continue
		}
		for _, key := range []string{"turns", "compactions", "max_context_tokens", "context_tokens"} {
			if _, present := task[key]; present {
				t.Errorf("no-stats task serialises a %q key; want it omitted", key)
			}
		}
	}
}

// TestReportTaskUsageOldDaemonMissingRunStatsCompat is the first of the two
// RUYI-154 compatibility cases: an old daemon build's JSON payload has no
// turns / compactions / max_context_tokens fields at all. The server must
// still accept the report and persist NULL for the three new columns rather
// than erroring or blocking the rest of the usage report (input/output/cache
// tokens, which the old daemon does send).
func TestReportTaskUsageOldDaemonMissingRunStatsCompat(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_, taskID := seedNULTask(t, "old-daemon-usage-agent")

	// Hand-built JSON body with the exact shape a pre-RUYI-154 daemon sends:
	// no turns/compactions/max_context_tokens keys at all, not even zeroed.
	body := []byte(`{"usage":[{
		"provider": "anthropic",
		"model": "claude-opus-5",
		"input_tokens": 1000,
		"output_tokens": 100,
		"cache_read_tokens": 0,
		"cache_write_tokens": 0
	}]}`)

	req := daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, json.RawMessage(body))
	w := httptest.NewRecorder()
	testHandler.ReportTaskUsage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ReportTaskUsage returned %d, want 200: %s", w.Code, w.Body.String())
	}

	var (
		inputTokens int64
		turns, compactions, maxContextTokens, contextTokens *int64
	)
	if err := testPool.QueryRow(ctx, `
		SELECT input_tokens, turns, compactions, max_context_tokens, context_tokens
		FROM task_usage WHERE task_id = $1`, taskID).
		Scan(&inputTokens, &turns, &compactions, &maxContextTokens, &contextTokens); err != nil {
		t.Fatalf("read persisted task usage: %v", err)
	}
	if inputTokens != 1000 {
		t.Errorf("input_tokens = %d, want 1000 (the old fields the daemon does send must still land)", inputTokens)
	}
	if turns != nil || compactions != nil || maxContextTokens != nil || contextTokens != nil {
		t.Errorf("run stats = turns=%v compactions=%v max_ctx=%v ctx=%v, want all NULL", turns, compactions, maxContextTokens, contextTokens)
	}
}
