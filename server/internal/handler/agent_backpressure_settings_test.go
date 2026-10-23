package handler

// RUYI-859 stage A: agent-level error-rate backpressure policy — the config
// surface and read-only visibility tests.
//
// Permission contract mirrors the scheduling freeze (RUYI-608): writes are
// human workspace owner/admin only (plain members and every machine
// credential fail closed), reads follow the agent's visibility. The card
// save is boundary-checked, stamped updated_by, and leaves an
// agent-domain audit trail (agent.suppression_config_updated) in the same
// transaction. The window reading counts only terminal samples
// (completed + failed with completed_at set); queued rows — including a
// runtime backpressure hold — have no completed_at and are structurally
// outside it, asserted here.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// backpressureAgentFixture: one public agent on the shared test runtime,
// with its policy/event/task rows removed at test end so a failing
// assertion cannot leak a card or samples into the next test in the same
// process.
func backpressureAgentFixture(t *testing.T) (agentID string) {
	t.Helper()
	agentID = dbfx.Agent(t, "bp-agent", handlerTestRuntimeID(t), testutil.Cols{
		"visibility": "workspace",
	})
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent_backpressure_policy WHERE agent_id = $1`, agentID)
		testPool.Exec(context.Background(),
			`DELETE FROM agent_suppression_event WHERE agent_id = $1`, agentID)
		testPool.Exec(context.Background(),
			`DELETE FROM agent_task_queue WHERE agent_id = $1`, agentID)
	})
	return agentID
}

// validBPBody is a minimal in-range card.
func validBPBody() map[string]any {
	return map[string]any{
		"window_minutes":           60,
		"error_rate_threshold_pct": 30,
		"min_samples":              5,
		"recovery_window_minutes":  30,
		"suppress_on_error_rate":   true,
		"count_model_api_failures": true,
		"count_execution_failures": true,
	}
}

func TestGetAgentBackpressurePolicy_DefaultsWhenUnconfigured(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := backpressureAgentFixture(t)

	w := httptest.NewRecorder()
	testHandler.GetAgentBackpressurePolicy(w, withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/backpressure-policy", nil),
		"id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d — %s", w.Code, w.Body.String())
	}
	var resp struct {
		Configured bool `json:"configured"`
		Config     struct {
			WindowMinutes         int     `json:"window_minutes"`
			ErrorRateThresholdPct float64 `json:"error_rate_threshold_pct"`
			MinSamples            int     `json:"min_samples"`
			RecoveryWindowMinutes int     `json:"recovery_window_minutes"`
			SuppressOnErrorRate   bool    `json:"suppress_on_error_rate"`
		} `json:"config"`
		Window *struct {
			TotalTerminal        int     `json:"total_terminal"`
			RecoveryThresholdPct float64 `json:"recovery_threshold_pct"`
		} `json:"window"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Configured {
		t.Fatal("fresh agent must answer configured=false")
	}
	// No row parses as the built-in defaults: monitor-only, auto off.
	if resp.Config.SuppressOnErrorRate {
		t.Fatalf("default card must have automatic suppression off — %+v", resp.Config)
	}
	if resp.Config.WindowMinutes != 60 || resp.Config.ErrorRateThresholdPct != 30 ||
		resp.Config.MinSamples != 5 || resp.Config.RecoveryWindowMinutes != 30 {
		t.Fatalf("default card numbers mismatch: %+v", resp.Config)
	}
	if resp.Window == nil || resp.Window.TotalTerminal != 0 {
		t.Fatalf("fresh agent has no terminal samples — window: %+v", resp.Window)
	}
	// The card must expose both windows and the FIXED 0.5x recovery
	// threshold (stage A product semantics): 30 * 0.5 = 15.
	if resp.Window.RecoveryThresholdPct != 15 {
		t.Fatalf("recovery_threshold_pct = %g, want 15 (0.5 x 30)", resp.Window.RecoveryThresholdPct)
	}
}

func TestPutAgentBackpressurePolicy_PermissionMatrix(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := backpressureAgentFixture(t)
	memberID := createPermissionTestMember(t, "bp-policy-member@multica.test")

	put := func(userID, actorSource string) *httptest.ResponseRecorder {
		req := withURLParam(newRequestAs(userID, http.MethodPut,
			"/api/agents/"+agentID+"/backpressure-policy", validBPBody()), "id", agentID)
		if actorSource != "" {
			req.Header.Set("X-Actor-Source", actorSource)
		}
		w := httptest.NewRecorder()
		testHandler.PutAgentBackpressurePolicy(w, req)
		return w
	}

	t.Run("workspace owner saves", func(t *testing.T) {
		if w := put(testUserID, ""); w.Code != http.StatusOK {
			t.Fatalf("owner PUT: got %d — %s", w.Code, w.Body.String())
		}
	})

	t.Run("plain member is rejected", func(t *testing.T) {
		if w := put(memberID, ""); w.Code != http.StatusForbidden {
			t.Fatalf("member PUT: got %d; want 403 — %s", w.Code, w.Body.String())
		}
	})

	t.Run("machine credential is rejected even for the owner", func(t *testing.T) {
		for _, source := range []string{"task_token", "cloud_pat", "oauth"} {
			if w := put(testUserID, source); w.Code != http.StatusForbidden {
				t.Fatalf("machine credential %s PUT: got %d; want 403 — %s", source, w.Code, w.Body.String())
			}
		}
	})
}

func TestPutAgentBackpressurePolicy_RejectsBoundaryViolations(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := backpressureAgentFixture(t)

	// Each body violates exactly one boundary rule; a reject must leave no
	// row behind.
	for name, body := range map[string]map[string]any{
		"zero window":      {"window_minutes": 0, "error_rate_threshold_pct": 30, "min_samples": 5, "recovery_window_minutes": 30},
		"zero threshold":   {"window_minutes": 60, "error_rate_threshold_pct": 0, "min_samples": 5, "recovery_window_minutes": 30},
		"over 100":         {"window_minutes": 60, "error_rate_threshold_pct": 101, "min_samples": 5, "recovery_window_minutes": 30},
		"zero min_samples": {"window_minutes": 60, "error_rate_threshold_pct": 30, "min_samples": 0, "recovery_window_minutes": 30},
		"zero recovery":    {"window_minutes": 60, "error_rate_threshold_pct": 30, "min_samples": 5, "recovery_window_minutes": 0},
	} {
		w := httptest.NewRecorder()
		testHandler.PutAgentBackpressurePolicy(w, withURLParam(newRequestAs(testUserID, http.MethodPut,
			"/api/agents/"+agentID+"/backpressure-policy", body), "id", agentID))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d; want 400 — %s", name, w.Code, w.Body.String())
		}
	}
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_backpressure_policy WHERE agent_id = $1`, agentID).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("rejected cards must not leave a row behind, found %d", n)
	}
}

func TestPutAgentBackpressurePolicy_AuditsInTransaction(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := backpressureAgentFixture(t)

	put := func(body map[string]any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		testHandler.PutAgentBackpressurePolicy(w, withURLParam(newRequestAs(testUserID, http.MethodPut,
			"/api/agents/"+agentID+"/backpressure-policy", body), "id", agentID))
		return w
	}
	if w := put(validBPBody()); w.Code != http.StatusOK {
		t.Fatalf("first PUT: got %d — %s", w.Code, w.Body.String())
	}
	body := validBPBody()
	body["window_minutes"] = 45
	if w := put(body); w.Code != http.StatusOK {
		t.Fatalf("second PUT: got %d — %s", w.Code, w.Body.String())
	}
	// Delete restores the defaults and is audited like a save.
	del := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		testHandler.DeleteAgentBackpressurePolicy(w, withURLParam(newRequestAs(testUserID, http.MethodDelete,
			"/api/agents/"+agentID+"/backpressure-policy", nil), "id", agentID))
		return w
	}
	if w := del(); w.Code != http.StatusOK {
		t.Fatalf("DELETE: got %d — %s", w.Code, w.Body.String())
	}

	var auditCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_event
		 WHERE workspace_id = $1 AND event_type = 'agent.suppression_config_updated'
		   AND agent_id = $2`, testWorkspaceID, agentID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 3 {
		t.Fatalf("audit rows = %d, want 3 (two saves + one delete)", auditCount)
	}
	// And no row survives the delete.
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_backpressure_policy WHERE agent_id = $1`, agentID).Scan(&n); err != nil {
		t.Fatalf("count policy rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("policy row must be gone after DELETE, found %d", n)
	}
}

// insertBPTerminalTask seeds one terminal (completed or failed) sample for
// agentID and registers its cleanup. reason '' = a success.
func insertBPTerminalTask(t *testing.T, agentID, status, reason string) {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, failure_reason, completed_at)
		VALUES ($1, (SELECT runtime_id FROM agent WHERE id = $1), $2, 0, $3, now())
		RETURNING id`, agentID, status, reason).Scan(&id); err != nil {
		t.Fatalf("insert terminal task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, id)
	})
}

// TestGetAgentBackpressurePolicy_WindowReadingExcludesQueued pins the core
// stage A contract: the terminal error-rate math only sees completed +
// failed rows with a completed_at. A queued row held by the runtime
// backpressure gate (its hold is the runtime's metadata report, not a task
// flag) can never be a failure sample, and must not inflate the
// denominator.
func TestGetAgentBackpressurePolicy_WindowReadingExcludesQueued(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	// A dedicated runtime carrying a live backpressure hold: the hold is
	// what keeps the queued task row sitting in the queue.
	runtimeID := createClaimReclaimRuntime(t, ctx, "bp-hold-rt")
	agentID := dbfx.Agent(t, "bp-hold-agent", runtimeID, testutil.Cols{"visibility": "workspace"})
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE agent_id = $1`, agentID)
	})
	seedStoredBackpressure(t, ctx, runtimeID, "30 seconds")
	// Two queued rows on the held runtime — the hold shape.
	queuedA := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	queuedB := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "priority": 1})
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, queuedA)
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, queuedB)
	})

	// 3 succeeded + 2 failed terminal samples in the entry window.
	for i := 0; i < 3; i++ {
		insertBPTerminalTask(t, agentID, "completed", "")
	}
	// model/API class (rate_limit fold) + execution class (runtime fold).
	insertBPTerminalTask(t, agentID, "failed", "agent_error.provider_capacity_or_rate_limit")
	insertBPTerminalTask(t, agentID, "failed", "runtime_offline")

	w := httptest.NewRecorder()
	testHandler.GetAgentBackpressurePolicy(w, withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/backpressure-policy", nil),
		"id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d — %s", w.Code, w.Body.String())
	}
	var resp struct {
		Window struct {
			TotalTerminal    int `json:"total_terminal"`
			SelectedFailures int `json:"selected_failures"`
			ErrorRatePct     *float64 `json:"error_rate_pct"`
		} `json:"window"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// 5 terminal samples (3 succeeded + 2 failed); the 2 queued rows on
	// the held runtime are NOT in either figure.
	if resp.Window.TotalTerminal != 5 {
		t.Fatalf("total_terminal = %d, want 5 (queued excluded)", resp.Window.TotalTerminal)
	}
	if resp.Window.SelectedFailures != 2 {
		t.Fatalf("selected_failures = %d, want 2 (both class switches on)", resp.Window.SelectedFailures)
	}
	if resp.Window.ErrorRatePct == nil || *resp.Window.ErrorRatePct != 40 {
		t.Fatalf("error_rate_pct = %v, want 40 (2/5)", resp.Window.ErrorRatePct)
	}
}

func TestListAgentBackpressureEvents_NewestFirst(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := backpressureAgentFixture(t)

	insertEvent := func(eventType string, ageMinutes int) {
		t.Helper()
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO agent_suppression_event
				(workspace_id, agent_id, event_type, sample_count, error_rate_pct,
				 effective_policy, occurred_at)
			VALUES ($1, $2, $3, 5, 40, '{}'::jsonb, now() - ($4::text || ' minutes')::interval)
		`, testWorkspaceID, agentID, eventType, fmt.Sprintf("%d", ageMinutes)); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	insertEvent("entered", 60)
	insertEvent("cleared", 10)

	w := httptest.NewRecorder()
	testHandler.ListAgentBackpressureEvents(w, withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/backpressure-events", nil),
		"id", agentID))
	if w.Code != http.StatusOK {
		t.Fatalf("GET events: got %d — %s", w.Code, w.Body.String())
	}
	var events []struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode bare array: %v — %s", err, w.Body.String())
	}
	if len(events) != 2 || events[0].EventType != "cleared" || events[1].EventType != "entered" {
		t.Fatalf("events = %+v, want cleared then entered (newest-first)", events)
	}
}
