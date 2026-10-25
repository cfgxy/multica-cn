package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestReportTaskModelCallEvidenceRequiresDaemonCredential(t *testing.T) {
	req := withURLParam(newRequestAs(testUserID, http.MethodPost, "/api/daemon/tasks/not-a-uuid/usage/evidence", nil), "taskId", "not-a-uuid")
	testutil.Call(t, (&Handler{}).ReportTaskModelCallEvidence, req).Want(http.StatusForbidden)
}

func TestModelCallBudgetActive_RequiresCompleteKnownWindow(t *testing.T) {
	config := runtimeModelCallBudgetConfig{Enabled: true, MaxCalls: 3, WindowSeconds: 3600}
	cases := []struct {
		name    string
		config  runtimeModelCallBudgetConfig
		known   int64
		unknown int64
		want    bool
	}{
		{name: "disabled budget never holds", config: runtimeModelCallBudgetConfig{MaxCalls: 3, WindowSeconds: 3600}, known: 3, want: false},
		{name: "known calls below maximum", config: config, known: 2, want: false},
		{name: "known calls at maximum", config: config, known: 3, want: true},
		{name: "known calls above maximum", config: config, known: 4, want: true},
		{name: "unknown only window fails open", config: config, unknown: 1, want: false},
		{name: "mixed known unknown window fails open", config: config, known: 99, unknown: 1, want: false},
		{name: "invalid maximum fails open", config: runtimeModelCallBudgetConfig{Enabled: true, MaxCalls: 0, WindowSeconds: 3600}, known: 1, want: false},
		{name: "invalid window fails open", config: runtimeModelCallBudgetConfig{Enabled: true, MaxCalls: 1, WindowSeconds: 0}, known: 1, want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := modelCallBudgetActive(tt.config, tt.known, tt.unknown); got != tt.want {
				t.Fatalf("modelCallBudgetActive(%+v, %d, %d) = %v, want %v", tt.config, tt.known, tt.unknown, got, tt.want)
			}
		})
	}
}

func TestValidateRuntimeModelCallBudgetConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		card runtimeModelCallBudgetConfig
		want bool
	}{
		{name: "minimum", card: runtimeModelCallBudgetConfig{MaxCalls: 1, WindowSeconds: minRuntimeModelCallBudgetWindowSeconds}, want: true},
		{name: "maximum", card: runtimeModelCallBudgetConfig{MaxCalls: maxRuntimeModelCallBudgetMaxCalls, WindowSeconds: maxRuntimeModelCallBudgetWindowSeconds}, want: true},
		{name: "zero calls", card: runtimeModelCallBudgetConfig{MaxCalls: 0, WindowSeconds: 3600}},
		{name: "too many calls", card: runtimeModelCallBudgetConfig{MaxCalls: maxRuntimeModelCallBudgetMaxCalls + 1, WindowSeconds: 3600}},
		{name: "short window", card: runtimeModelCallBudgetConfig{MaxCalls: 1, WindowSeconds: minRuntimeModelCallBudgetWindowSeconds - 1}},
		{name: "long window", card: runtimeModelCallBudgetConfig{MaxCalls: 1, WindowSeconds: maxRuntimeModelCallBudgetWindowSeconds + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := validateRuntimeModelCallBudgetConfig(tc.card) == nil
			if got != tc.want {
				t.Fatalf("validateRuntimeModelCallBudgetConfig(%+v) valid=%v, want %v", tc.card, got, tc.want)
			}
		})
	}
}

func runtimeModelCallBudgetFixture(t *testing.T, name string) string {
	t.Helper()
	runtimeID := dbfx.Runtime(t, "model-call-budget-"+name, testutil.Cols{
		"daemon_id":    "model-call-budget-daemon-" + name,
		"runtime_mode": "local",
		"visibility":   "public",
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `
			DELETE FROM audit_event
			WHERE workspace_id = $1
			  AND event_type = 'ops.runtime_model_call_budget_updated'
			  AND details->>'runtime_id' = $2`, testWorkspaceID, runtimeID)
	})
	return runtimeID
}

func modelCallEvidenceRequest(t *testing.T, runtimeID, taskID, body string) *http.Request {
	t.Helper()
	var daemonID string
	if err := testPool.QueryRow(context.Background(), `SELECT daemon_id FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&daemonID); err != nil {
		t.Fatalf("load runtime daemon id: %v", err)
	}
	return modelCallEvidenceRequestAs(t, taskID, body, daemonID)
}

func modelCallEvidenceRequestAs(t *testing.T, taskID, body, daemonID string) *http.Request {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/usage/evidence", json.RawMessage(body), testWorkspaceID, daemonID)
	return withURLParam(req, "taskId", taskID)
}

func modelCallBudgetBody(enabled bool, maxCalls int64, windowSeconds int32) map[string]any {
	return map[string]any{
		"enabled":        enabled,
		"max_calls":      maxCalls,
		"window_seconds": windowSeconds,
	}
}

func putRuntimeModelCallBudgetForTest(t *testing.T, runtimeID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.PutRuntimeModelCallBudget(w, withURLParam(newRequestAs(testUserID, http.MethodPut,
		"/api/runtimes/"+runtimeID+"/model-call-budget", body), "runtimeId", runtimeID))
	return w
}

func setRuntimeModelCallBudgetForTest(t *testing.T, runtimeID string, enabled bool, maxCalls int64, windowSeconds int32) {
	t.Helper()
	if w := putRuntimeModelCallBudgetForTest(t, runtimeID, modelCallBudgetBody(enabled, maxCalls, windowSeconds)); w.Code != http.StatusOK {
		t.Fatalf("save model-call budget: got %d: %s", w.Code, w.Body.String())
	}
}

func seedModelCallUsage(t *testing.T, ctx context.Context, taskID, provider, model string, calls *int64) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, model_call_count)
		VALUES ($1, $2, $3, 1, $4)`, taskID, provider, model, calls); err != nil {
		t.Fatalf("seed model-call usage: %v", err)
	}
	if calls != nil {
		for i := int64(0); i < *calls; i++ {
			if _, err := testPool.Exec(ctx, `
				INSERT INTO task_usage_model_call_evidence (task_id, provider, model, evidence_id)
				VALUES ($1, $2, $3, $4)`, taskID, provider, model, fmt.Sprintf("budget-seed-%s-%d", model, i)); err != nil {
				t.Fatalf("seed model-call evidence: %v", err)
			}
		}
	}
}

func completeTaskForModelCallBudget(t *testing.T, ctx context.Context, taskID string) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now()
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("complete usage fixture task: %v", err)
	}
}

func TestRuntimeModelCallBudget_GetDefaultsAndSaveAudits(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler tests require a database")
	}
	runtimeID := runtimeModelCallBudgetFixture(t, "api")

	w := httptest.NewRecorder()
	testHandler.GetRuntimeModelCallBudget(w, withURLParam(newRequestAs(testUserID, http.MethodGet,
		"/api/runtimes/"+runtimeID+"/model-call-budget", nil), "runtimeId", runtimeID))
	if w.Code != http.StatusOK {
		t.Fatalf("GET default: got %d: %s", w.Code, w.Body.String())
	}
	var initial struct {
		Config runtimeModelCallBudgetConfig `json:"config"`
		Report runtimeModelCallBudgetReport `json:"report"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode default response: %v", err)
	}
	if initial.Config.Enabled || initial.Config.MaxCalls != defaultRuntimeModelCallBudgetMaxCalls || initial.Config.WindowSeconds != defaultRuntimeModelCallBudgetWindowSeconds {
		t.Fatalf("default config = %+v", initial.Config)
	}
	if !initial.Report.Complete || initial.Report.Active || initial.Report.KnownCalls != 0 || initial.Report.UnknownUsageRows != 0 || initial.Report.WindowStartedAt == "" {
		t.Fatalf("default report = %+v", initial.Report)
	}

	if w := putRuntimeModelCallBudgetForTest(t, runtimeID, modelCallBudgetBody(true, 42, 3600)); w.Code != http.StatusOK {
		t.Fatalf("PUT: got %d: %s", w.Code, w.Body.String())
	}
	var enabled bool
	var maxCalls int64
	var windowSeconds int32
	if err := testPool.QueryRow(context.Background(), `
		SELECT model_call_budget_enabled, model_call_budget_max_calls, model_call_budget_window_seconds
		FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&enabled, &maxCalls, &windowSeconds); err != nil {
		t.Fatalf("load saved budget: %v", err)
	}
	if !enabled || maxCalls != 42 || windowSeconds != 3600 {
		t.Fatalf("saved config = enabled=%v max=%d window=%d", enabled, maxCalls, windowSeconds)
	}
	var audits int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit_event
		WHERE workspace_id = $1
		  AND event_type = 'ops.runtime_model_call_budget_updated'
		  AND details->>'runtime_id' = $2`, testWorkspaceID, runtimeID).Scan(&audits); err != nil {
		t.Fatalf("count budget audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("budget audit rows = %d, want 1", audits)
	}
}

func TestPutRuntimeModelCallBudget_ValidatesAndRestrictsWrites(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler tests require a database")
	}
	runtimeID := runtimeModelCallBudgetFixture(t, "validation")
	memberID := createPermissionTestMember(t, "model-call-budget-member@multica.test")

	call := func(userID, actorSource string, body map[string]any) *httptest.ResponseRecorder {
		req := withURLParam(newRequestAs(userID, http.MethodPut,
			"/api/runtimes/"+runtimeID+"/model-call-budget", body), "runtimeId", runtimeID)
		if actorSource != "" {
			req.Header.Set("X-Actor-Source", actorSource)
		}
		w := httptest.NewRecorder()
		testHandler.PutRuntimeModelCallBudget(w, req)
		return w
	}
	if w := call(memberID, "", modelCallBudgetBody(true, 42, 3600)); w.Code != http.StatusForbidden {
		t.Fatalf("member PUT: got %d; want 403: %s", w.Code, w.Body.String())
	}
	if w := call(testUserID, "task_token", modelCallBudgetBody(true, 42, 3600)); w.Code != http.StatusForbidden {
		t.Fatalf("machine credential PUT: got %d; want 403: %s", w.Code, w.Body.String())
	}
	for _, body := range []map[string]any{
		modelCallBudgetBody(true, 0, 3600),
		modelCallBudgetBody(true, 42, minRuntimeModelCallBudgetWindowSeconds-1),
		modelCallBudgetBody(true, maxRuntimeModelCallBudgetMaxCalls+1, 3600),
	} {
		if w := call(testUserID, "", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid budget %v: got %d; want 400: %s", body, w.Code, w.Body.String())
		}
	}
}

func TestRuntimeModelCallBudget_ClaimPathsFailOpenForUnknown(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler tests require a database")
	}
	ctx := context.Background()

	t.Run("single runtime holds known exhausted then releases mixed window", func(t *testing.T) {
		runtimeID := runtimeModelCallBudgetFixture(t, "single")
		agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "model-call-budget-single")
		usageTaskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		completeTaskForModelCallBudget(t, ctx, usageTaskID)
		knownCalls := int64(2)
		seedModelCallUsage(t, ctx, usageTaskID, "claude", "known", &knownCalls)
		queuedID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		setRuntimeModelCallBudgetForTest(t, runtimeID, true, 2, 3600)

		if task, body := claimTaskByRuntimeForTest(t, runtimeID); task != nil {
			t.Fatalf("known exhausted runtime claimed %s, want nil: %s", task.ID, body)
		}
		if got := queueStatus(t, ctx, queuedID); got != "queued" {
			t.Fatalf("held task status = %q, want queued", got)
		}

		// One unknown row makes the whole rolling window incomplete, so the
		// partial known total must not continue holding this runtime.
		seedModelCallUsage(t, ctx, usageTaskID, "copilot", "unknown", nil)
		if task, body := claimTaskByRuntimeForTest(t, runtimeID); task == nil || task.ID != queuedID {
			t.Fatalf("mixed known/unknown window must fail open, got %+v: %s", task, body)
		}
	})

	t.Run("single runtime claims after the complete known window expires", func(t *testing.T) {
		runtimeID := runtimeModelCallBudgetFixture(t, "single-window-expiry")
		agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "model-call-budget-single-window-expiry")
		usageTaskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		completeTaskForModelCallBudget(t, ctx, usageTaskID)
		knownCalls := int64(1)
		seedModelCallUsage(t, ctx, usageTaskID, "claude", "known", &knownCalls)
		queuedID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		setRuntimeModelCallBudgetForTest(t, runtimeID, true, 1, 60)

		if task, body := claimTaskByRuntimeForTest(t, runtimeID); task != nil {
			t.Fatalf("known window before expiry claimed %s, want nil: %s", task.ID, body)
		}
		if got := queueStatus(t, ctx, queuedID); got != "queued" {
			t.Fatalf("held task status = %q, want queued", got)
		}

		// Anchor the row to the database clock instead of sleeping through the
		// minimum budget window. The handler's current-time query must then
		// exclude this complete known usage and release the queued task.
		if _, err := testPool.Exec(ctx, `
				UPDATE task_usage_model_call_evidence
				SET created_at = now() - INTERVAL '61 seconds'
				WHERE task_id = $1`, usageTaskID); err != nil {
			t.Fatalf("move evidence outside model-call budget window: %v", err)
		}
		if task, body := claimTaskByRuntimeForTest(t, runtimeID); task == nil || task.ID != queuedID {
			t.Fatalf("expired complete known window must claim %s, got %+v: %s", queuedID, task, body)
		}
	})

	t.Run("running task evidence holds then expires without terminal usage", func(t *testing.T) {
		runtimeID := runtimeModelCallBudgetFixture(t, "running-evidence")
		agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "model-call-budget-running-evidence")
		usageTaskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, usageTaskID); err != nil {
			t.Fatalf("mark source task running: %v", err)
		}
		queuedID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		setRuntimeModelCallBudgetForTest(t, runtimeID, true, 1, 60)
		crossDaemonReq := modelCallEvidenceRequestAs(t, usageTaskID,
			`{"provider":"claude","model":"budget-live","evidence_id":"cross-daemon-call"}`, "another-daemon")
		testutil.Call(t, testHandler.ReportTaskModelCallEvidence, crossDaemonReq).Want(http.StatusForbidden)
		req := modelCallEvidenceRequest(t, runtimeID, usageTaskID,
			`{"provider":"claude","model":"budget-live","evidence_id":"live-call-1"}`)
		testutil.Call(t, testHandler.ReportTaskModelCallEvidence, req).Want(http.StatusOK)

		if task, body := claimTaskByRuntimeForTest(t, runtimeID); task != nil {
			t.Fatalf("running evidence did not hold runtime: claimed %s: %s", task.ID, body)
		}
		var evidenceCount, usageCount int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_usage_model_call_evidence WHERE task_id = $1`, usageTaskID).Scan(&evidenceCount); err != nil {
			t.Fatalf("count running evidence: %v", err)
		}
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_usage WHERE task_id = $1`, usageTaskID).Scan(&usageCount); err != nil {
			t.Fatalf("count terminal usage: %v", err)
		}
		if evidenceCount != 1 || usageCount != 0 {
			t.Fatalf("running persistence = %d evidence / %d usage rows, want 1 / 0", evidenceCount, usageCount)
		}
		if _, err := testPool.Exec(ctx, `UPDATE task_usage_model_call_evidence SET created_at = now() - INTERVAL '61 seconds' WHERE task_id = $1`, usageTaskID); err != nil {
			t.Fatalf("expire live evidence: %v", err)
		}
		if task, body := claimTaskByRuntimeForTest(t, runtimeID); task == nil || task.ID != queuedID {
			t.Fatalf("expired running evidence must release queued task %s, got %+v: %s", queuedID, task, body)
		}
	})

	t.Run("running incomplete evidence fails open across a task retry", func(t *testing.T) {
		runtimeID := runtimeModelCallBudgetFixture(t, "running-incomplete")
		agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "model-call-budget-running-incomplete")
		knownTaskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		completeTaskForModelCallBudget(t, ctx, knownTaskID)
		knownCalls := int64(1)
		seedModelCallUsage(t, ctx, knownTaskID, "claude", "known", &knownCalls)
		incompleteTaskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1`, incompleteTaskID); err != nil {
			t.Fatalf("mark incomplete source task running: %v", err)
		}
		queuedID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		setRuntimeModelCallBudgetForTest(t, runtimeID, true, 1, 60)

		req := modelCallEvidenceRequest(t, runtimeID, incompleteTaskID,
			`{"provider":"claude","model":"unknown-live","incomplete":true}`)
		testutil.Call(t, testHandler.ReportTaskModelCallEvidence, req).Want(http.StatusOK)
		if _, err := testPool.Exec(ctx, `UPDATE task_usage SET created_at = now() - INTERVAL '2 hours' WHERE task_id = $1`, incompleteTaskID); err != nil {
			t.Fatalf("age incomplete usage creation time: %v", err)
		}
		if task, body := claimTaskByRuntimeForTest(t, runtimeID); task == nil || task.ID != queuedID {
			t.Fatalf("recent incomplete evidence must fail open and claim %s, got %+v: %s", queuedID, task, body)
		}
	})

	t.Run("batch runtime holds known exhausted then releases unknown window", func(t *testing.T) {
		runtimeID := runtimeModelCallBudgetFixture(t, "batch")
		agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "model-call-budget-batch")
		usageTaskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		completeTaskForModelCallBudget(t, ctx, usageTaskID)
		knownCalls := int64(3)
		seedModelCallUsage(t, ctx, usageTaskID, "claude", "known", &knownCalls)
		queuedID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
		setRuntimeModelCallBudgetForTest(t, runtimeID, true, 3, 3600)

		w := postBatchClaim(t, testWorkspaceID, []string{runtimeID}, 5)
		if w.Code != http.StatusOK {
			t.Fatalf("batch known hold: got %d: %s", w.Code, w.Body.String())
		}
		if resp := decodeBatchClaim(t, w); len(resp.Tasks) != 0 {
			t.Fatalf("known exhausted batch runtime claimed %+v", resp.Tasks)
		}
		if got := queueStatus(t, ctx, queuedID); got != "queued" {
			t.Fatalf("batch held task status = %q, want queued", got)
		}

		seedModelCallUsage(t, ctx, usageTaskID, "copilot", "unknown", nil)
		w = postBatchClaim(t, testWorkspaceID, []string{runtimeID}, 5)
		if w.Code != http.StatusOK {
			t.Fatalf("batch unknown release: got %d: %s", w.Code, w.Body.String())
		}
		resp := decodeBatchClaim(t, w)
		if len(resp.Tasks) != 1 || resp.Tasks[0].ID != queuedID {
			t.Fatalf("unknown batch window must fail open, got %+v", resp.Tasks)
		}
	})
}

func TestListTasksByIssue_QueuedReasonModelCallBudget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler tests require a database")
	}
	ctx := context.Background()
	runtimeID := runtimeModelCallBudgetFixture(t, "queued-reason")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "model-call-budget-queued-reason")
	usageTaskID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	completeTaskForModelCallBudget(t, ctx, usageTaskID)
	knownCalls := int64(1)
	seedModelCallUsage(t, ctx, usageTaskID, "claude", "known", &knownCalls)
	queuedID := seedQueuedIssueTask(t, ctx, agentID, runtimeID, issueID)
	setRuntimeModelCallBudgetForTest(t, runtimeID, true, 1, 3600)

	runs := func() map[string]AgentTaskResponse {
		resp := testutil.Call(t, testHandler.ListTasksByIssue,
			withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/task-runs", nil), "id", issueID),
		).Want(http.StatusOK)
		var rows []AgentTaskResponse
		resp.JSON(&rows)
		byID := make(map[string]AgentTaskResponse, len(rows))
		for _, row := range rows {
			byID[row.ID] = row
		}
		return byID
	}
	if got := runs()[queuedID].QueuedReason; got != "runtime_model_call_budget" {
		t.Fatalf("queued reason = %q, want runtime_model_call_budget", got)
	}

	// An unknown row has precedence over a partial sum: observable in the
	// budget report, but never rendered as an admission hold.
	seedModelCallUsage(t, ctx, usageTaskID, "copilot", "unknown", nil)
	if got := runs()[queuedID].QueuedReason; got != "" {
		t.Fatalf("unknown budget window still carries queued reason %q", got)
	}
}
