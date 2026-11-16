package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/agentconfig"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
		inputTokens                                         int64
		turns, compactions, maxContextTokens, contextTokens *int64
		attributionKnown                                    bool
		sessionID                                           string
	)
	if err := testPool.QueryRow(ctx, `
		SELECT input_tokens, turns, compactions, max_context_tokens, context_tokens,
			session_id, session_attribution_known
		FROM task_usage WHERE task_id = $1`, taskID).
		Scan(&inputTokens, &turns, &compactions, &maxContextTokens, &contextTokens,
			&sessionID, &attributionKnown); err != nil {
		t.Fatalf("read persisted task usage: %v", err)
	}
	if inputTokens != 1000 {
		t.Errorf("input_tokens = %d, want 1000 (the old fields the daemon does send must still land)", inputTokens)
	}
	if turns != nil || compactions != nil || maxContextTokens != nil || contextTokens != nil {
		t.Errorf("run stats = turns=%v compactions=%v max_ctx=%v ctx=%v, want all NULL", turns, compactions, maxContextTokens, contextTokens)
	}
	if sessionID != "" || attributionKnown {
		t.Errorf("old daemon attribution = session_id=%q known=%v, want empty/unknown", sessionID, attributionKnown)
	}
}

func TestReportTaskUsageContextOnlyReachesSessionGate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	_, taskID := seedNULTask(t, "context-only-usage-agent")
	taskUUID := parseUUID(taskID)
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM task_usage WHERE task_id = $1`, taskID); err != nil {
			t.Errorf("clean up task usage: %v", err)
		}
	})

	body := json.RawMessage(`{"usage":[{"provider":"anthropic","model":"claude-sonnet-5","input_tokens":0,"output_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"context_tokens":350000}]}`)
	req := daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, body)
	testutil.Call(t, testHandler.ReportTaskUsage, req).Want(http.StatusOK)

	rows, err := testHandler.Queries.GetTaskUsage(ctx, taskUUID)
	if err != nil {
		t.Fatalf("read task usage: %v", err)
	}
	if len(rows) != 1 || rows[0].InputTokens != 0 || rows[0].OutputTokens != 0 ||
		rows[0].CacheReadTokens != 0 || rows[0].CacheWriteTokens != 0 ||
		!rows[0].ContextTokens.Valid || rows[0].ContextTokens.Int64 != 350000 {
		t.Fatalf("persisted context-only usage = %+v, want one zero-billing row with context 350000", rows)
	}

	tokens, err := testHandler.Queries.GetTaskContextTokens(ctx, taskUUID)
	if err != nil || !tokens.Valid || tokens.Int64 != 350000 {
		t.Fatalf("GetTaskContextTokens = %+v, %v; want 350000", tokens, err)
	}
	outcome := testHandler.decideSessionResumeForTask(ctx, db.Agent{
		SessionMaxContextTokens: 400000,
		SessionCompactPct:       80,
	}, taskUUID)
	if !outcome.known || outcome.contextTokens != tokens.Int64 || outcome.decision != agentconfig.SessionResumeCompactSoft {
		t.Fatalf("session gate = %+v, want measured 350000 and compact_soft", outcome)
	}
}

func TestReportTaskUsagePreservesRetrySessionAttribution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, taskID := seedNULTask(t, "session-attribution-agent")

	var issueID string
	if err := testPool.QueryRow(ctx, `SELECT issue_id FROM agent_task_queue WHERE id = $1`, taskID).Scan(&issueID); err != nil {
		t.Fatalf("load task issue: %v", err)
	}

	postUsage := func(sessionID string, input int64) {
		t.Helper()
		body := map[string]any{"usage": []map[string]any{{
			"session_id":   sessionID,
			"provider":     "claude",
			"model":        "claude-sonnet-5",
			"input_tokens": input,
		}}}
		testutil.Call(t, testHandler.ReportTaskUsage,
			daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, body),
		).Want(http.StatusOK)
	}

	// The resumed attempt contributes 50 to the old session, while the fresh
	// retry contributes 10 to its own session under the same task and model.
	postUsage("old-session", 50)
	postUsage("fresh-session", 10)

	var rows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM task_usage
		WHERE task_id = $1 AND provider = 'claude' AND model = 'claude-sonnet-5'`, taskID).Scan(&rows); err != nil {
		t.Fatalf("count attributed usage rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("attributed usage rows = %d, want 2", rows)
	}
	var knownRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM task_usage
		WHERE task_id = $1 AND provider = 'claude' AND model = 'claude-sonnet-5'
		  AND session_attribution_known = TRUE`, taskID).Scan(&knownRows); err != nil {
		t.Fatalf("count known attributed usage rows: %v", err)
	}
	if knownRows != 2 {
		t.Fatalf("known attributed usage rows = %d, want 2", knownRows)
	}

	// Simulate the terminal callback rewriting the task's current resume pointer,
	// then let the real claim endpoint read the fresh-session baseline.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now(), session_id = $2
		WHERE id = $1`, taskID, "fresh-session"); err != nil {
		t.Fatalf("complete retry task: %v", err)
	}

	var nextTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, session_id)
		SELECT agent_id, runtime_id, issue_id, 'queued', 0, 'fresh-session'
		FROM agent_task_queue WHERE id = $1
		RETURNING id`, taskID).Scan(&nextTaskID); err != nil {
		t.Fatalf("create next task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, nextTaskID)
	})

	prior, err := testHandler.Queries.GetPriorSessionUsage(ctx, db.GetPriorSessionUsageParams{
		SessionID: pgtype.Text{String: "fresh-session", Valid: true},
		AgentID:   parseUUID(agentID),
		ID:        parseUUID(nextTaskID),
	})
	if err != nil {
		t.Fatalf("load fresh-session baseline: %v", err)
	}
	if len(prior) != 1 || prior[0].InputTokens != 10 {
		t.Fatalf("fresh-session baseline = %+v, want one row with 10 input tokens", prior)
	}

	runtimeID := handlerTestRuntimeID(t)
	claimRequest := newDaemonTokenRequest(
		"POST",
		"/api/daemon/runtimes/"+runtimeID+"/claim",
		nil,
		testWorkspaceID,
		"session-attribution-daemon",
	)
	claimContext := chi.NewRouteContext()
	claimContext.URLParams.Add("runtimeId", runtimeID)
	claimRequest = claimRequest.WithContext(context.WithValue(claimRequest.Context(), chi.RouteCtxKey, claimContext))
	claimResponse := httptest.NewRecorder()
	testHandler.ClaimTaskByRuntime(claimResponse, claimRequest)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime returned %d: %s", claimResponse.Code, claimResponse.Body.String())
	}
	var claimed struct {
		Task *struct {
			PriorSessionID    string          `json:"prior_session_id"`
			PriorSessionUsage []TaskUsageData `json:"prior_session_usage"`
			UsageUnavailable  bool            `json:"prior_session_usage_unavailable"`
		} `json:"task"`
	}
	if err := json.Unmarshal(claimResponse.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if claimed.Task == nil {
		t.Fatalf("claim response has no task: %s", claimResponse.Body.String())
	}
	if claimed.Task.PriorSessionID != "fresh-session" || claimed.Task.UsageUnavailable {
		t.Fatalf("claimed session baseline metadata = %+v, want fresh-session and available", claimed.Task)
	}
	if len(claimed.Task.PriorSessionUsage) != 1 || claimed.Task.PriorSessionUsage[0].InputTokens != 10 {
		t.Fatalf("claimed fresh-session baseline = %+v, want one row with 10 input tokens", claimed.Task.PriorSessionUsage)
	}

	issueSummary, err := testHandler.Queries.GetIssueUsageSummary(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue usage summary: %v", err)
	}
	if issueSummary.TotalInputTokens != 60 {
		t.Fatalf("issue input total = %d, want 60 (old 50 + fresh 10)", issueSummary.TotalInputTokens)
	}
}

func TestUnknownTaskUsageFailsClosedForIssueSessionBaseline(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, priorTaskID := seedNULTask(t, "unknown-session-attribution-agent")

	var issueID, previousProvider string
	if err := testPool.QueryRow(ctx, `
		SELECT q.issue_id, r.provider
		FROM agent_task_queue q
		JOIN agent_runtime r ON r.id = q.runtime_id
		WHERE q.id = $1`, priorTaskID).Scan(&issueID, &previousProvider); err != nil {
		t.Fatalf("load task issue and runtime provider: %v", err)
	}
	// The claim gate checks the runtime provider. Make this fixture an ACP
	// provider so the handler response exercises the same provider key that the
	// daemon uses for cumulative-session usage.
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET provider = 'claude' WHERE id = (SELECT runtime_id FROM agent_task_queue WHERE id = $1)`, priorTaskID); err != nil {
		t.Fatalf("set fixture runtime provider: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			UPDATE agent_runtime SET provider = $2
			WHERE id = (SELECT runtime_id FROM agent_task_queue WHERE id = $1)`, priorTaskID, previousProvider)
	})

	// This is the old daemon wire shape: the usage entry has no session_id.
	// It must remain unattributed instead of being rebound to the task's later
	// session by the terminal callback.
	oldDaemonPayload := json.RawMessage(`{"usage":[{"provider":"claude","model":"claude-sonnet-5","input_tokens":60,"output_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0}]}`)
	testutil.Call(t, testHandler.ReportTaskUsage,
		daemonTaskRequest(t, "/api/daemon/tasks/"+priorTaskID+"/usage", priorTaskID, oldDaemonPayload),
	).Want(http.StatusOK)

	var storedSessionID string
	var storedAttributionKnown bool
	if err := testPool.QueryRow(ctx, `
		SELECT session_id, session_attribution_known
		FROM task_usage
		WHERE task_id = $1 AND provider = 'claude' AND model = 'claude-sonnet-5'`, priorTaskID).
		Scan(&storedSessionID, &storedAttributionKnown); err != nil {
		t.Fatalf("read old-daemon usage attribution: %v", err)
	}
	if storedSessionID != "" || storedAttributionKnown {
		t.Fatalf("old-daemon usage attribution = session %q, known=%v; want empty/unknown", storedSessionID, storedAttributionKnown)
	}

	// Drive the real terminal callback so TaskService, rather than a direct SQL
	// update, rewrites the task's current session pointer.
	completeReq := daemonTaskRequest(t, "/api/daemon/tasks/"+priorTaskID+"/complete", priorTaskID, map[string]any{
		"output":     "completed",
		"session_id": "fresh-session",
	})
	testutil.Call(t, testHandler.CompleteTask, completeReq).Want(http.StatusOK)

	var status, completedSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT status, session_id FROM agent_task_queue WHERE id = $1`, priorTaskID).
		Scan(&status, &completedSessionID); err != nil {
		t.Fatalf("read completed task: %v", err)
	}
	if status != "completed" || completedSessionID != "fresh-session" {
		t.Fatalf("completed task state = status %q, session %q; want completed/fresh-session", status, completedSessionID)
	}

	var nextTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, session_id)
		SELECT agent_id, runtime_id, issue_id, 'queued', 10000, 'fresh-session'
		FROM agent_task_queue WHERE id = $1
		RETURNING id`, priorTaskID).Scan(&nextTaskID); err != nil {
		t.Fatalf("create next task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM task_usage WHERE task_id = $1`, priorTaskID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, nextTaskID)
	})

	prior, err := testHandler.Queries.GetPriorSessionUsage(ctx, db.GetPriorSessionUsageParams{
		SessionID: pgtype.Text{String: "fresh-session", Valid: true},
		AgentID:   parseUUID(agentID),
		ID:        parseUUID(nextTaskID),
	})
	if err != nil {
		t.Fatalf("load fresh baseline: %v", err)
	}
	if len(prior) != 0 {
		t.Fatalf("unknown usage entered fresh baseline = %+v, want empty", prior)
	}

	unavailable, err := testHandler.Queries.HasUnattributedPriorSessionUsage(ctx, db.HasUnattributedPriorSessionUsageParams{
		ID:       parseUUID(nextTaskID),
		AgentID:  parseUUID(agentID),
		Provider: "claude",
	})
	if err != nil {
		t.Fatalf("check unknown usage marker: %v", err)
	}
	if !unavailable {
		t.Fatal("unknown usage did not mark the issue baseline unavailable")
	}

	runtimeID := handlerTestRuntimeID(t)
	claimReq := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/claim",
		nil,
		testWorkspaceID,
		"usage-attribution-claim-daemon",
	)
	claimReq = withURLParam(claimReq, "runtimeId", runtimeID)
	claimResponse := httptest.NewRecorder()
	testHandler.ClaimTaskByRuntime(claimResponse, claimReq)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime returned %d: %s", claimResponse.Code, claimResponse.Body.String())
	}
	var claimed struct {
		Task *AgentTaskResponse `json:"task"`
	}
	if err := json.Unmarshal(claimResponse.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if claimed.Task == nil {
		t.Fatalf("claim response has no task: %s", claimResponse.Body.String())
	}
	if claimed.Task.ID != nextTaskID {
		t.Fatalf("claimed task = %s, want %s", claimed.Task.ID, nextTaskID)
	}
	if claimed.Task.PriorSessionID != "fresh-session" {
		t.Fatalf("claimed prior session = %q, want fresh-session", claimed.Task.PriorSessionID)
	}
	if !claimed.Task.PriorSessionUsageUnavailable {
		t.Fatalf("claim did not propagate PriorSessionUsageUnavailable: %+v", claimed.Task)
	}
	if len(claimed.Task.PriorSessionUsage) != 0 {
		t.Fatalf("claim returned an unsafe fresh baseline = %+v, want empty", claimed.Task.PriorSessionUsage)
	}

	// normalizePriorSessionUsage returns nil for the next cumulative snapshot when
	// the baseline is unavailable. Client.ReportTaskUsage consequently sends an
	// empty usage array; exercise that real handler boundary rather than inserting
	// the expected absence directly into the database.
	reportReq := daemonTaskRequest(t, "/api/daemon/tasks/"+nextTaskID+"/usage", nextTaskID, json.RawMessage(`{"usage":[]}`))
	testutil.Call(t, testHandler.ReportTaskUsage, reportReq).Want(http.StatusOK)

	var currentRunRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM task_usage WHERE task_id = $1`, nextTaskID).Scan(&currentRunRows); err != nil {
		t.Fatalf("read unavailable cumulative snapshot rows: %v", err)
	}
	if currentRunRows != 0 {
		t.Fatalf("unavailable cumulative snapshot created %d current-run rows, want none", currentRunRows)
	}

	issueSummary, err := testHandler.Queries.GetIssueUsageSummary(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue usage summary: %v", err)
	}
	if issueSummary.TotalInputTokens != 60 {
		t.Fatalf("issue input total = %d, want 60 while baseline remains unavailable", issueSummary.TotalInputTokens)
	}
}
