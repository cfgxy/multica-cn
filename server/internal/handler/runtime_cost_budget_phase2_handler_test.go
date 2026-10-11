package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/dispatch"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// RUYI-665 phase 2 handler acceptance: the chat send refusal end to end
// (409 + reason_code + nothing persisted), the warn-only degradation switch
// observed through the same HTTP surface, and the owner-only PUT gate.

// seedBudgetSpend plants a spent daily budget on the suite runtime. The gate
// re-aggregates from task_usage over that runtime, so any completed task on it
// carries the spend.
func seedBudgetSpend(t *testing.T, dailyTicks, spentTicks int64) {
	t.Helper()
	spenderID := dbfx.Agent(t, "budget-spender-agent", handlerTestRuntimeID(t))
	taskID := dbfx.Task(t, spenderID, testutil.Cols{"runtime_id": testRuntimeID, "status": "completed"})
	dbfx.Insert(t, "task_usage", testutil.Cols{
		"task_id":        taskID,
		"provider":       "budget-test",
		"model":          "budget-test-model",
		"input_tokens":   0,
		"output_tokens":  0,
		"cost_usd_ticks": spentTicks,
	})
	dbfx.Insert(t, "runtime_cost_budget", testutil.Cols{
		"workspace_id":          testWorkspaceID,
		"runtime_id":            testRuntimeID,
		"daily_limit_usd_ticks": dailyTicks,
		"updated_by":            testUserID,
	})
}

func sendChatMessage(t *testing.T, sessionID, content string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := withChatTestWorkspaceCtx(t, newRequest("POST", "/api/chat/sessions/"+sessionID+"/messages", map[string]any{
		"content": content,
	}))
	req = withURLParam(req, "sessionId", sessionID)
	testHandler.SendChatMessage(w, req)
	return w
}

func countSessionRows(t *testing.T, sql string, sessionID string) int {
	t.Helper()
	return dbfx.Count(t, sql, sessionID)
}

// TestSendChatMessage_BudgetReachedRefusedBeforeAnythingPersists is the
// full-chain acceptance: the send reaches the service, the budget gate fires,
// and the handler answers the unified blocked-admission 409 with the
// amount-free reason — with no chat message and no task written.
func TestSendChatMessage_BudgetReachedRefusedBeforeAnythingPersists(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	seedBudgetSpend(t, 100, 100)
	agentID := createHandlerTestAgent(t, "budget-chat-agent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	w := sendChatMessage(t, sessionID, "hello through a spent budget")

	if w.Code != http.StatusConflict {
		t.Fatalf("SendChatMessage on spent budget: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if code := readReasonCode(t, w.Body.Bytes()); code != string(dispatch.ReasonBudgetExceeded) {
		t.Errorf("reason_code = %q, want budget_exceeded", code)
	}
	if strings.ContainsAny(w.Body.String(), "0123456789") {
		t.Errorf("refusal body %s carries digits; the wire reason must be amount-free", w.Body.String())
	}
	if got := countSessionRows(t, `SELECT count(*) FROM chat_message WHERE chat_session_id = $1`, sessionID); got != 0 {
		t.Errorf("refused send persisted %d chat messages, want 0", got)
	}
	if got := countSessionRows(t, `SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1`, sessionID); got != 0 {
		t.Errorf("refused send persisted %d tasks, want 0", got)
	}
}

// TestSendChatMessage_WarnOnlyAllowsTheSend flips the degradation switch and
// verifies the same request now succeeds, persists the message and the task,
// and still fires the once-per-period owner notice with the warn-only copy.
func TestSendChatMessage_WarnOnlyAllowsTheSend(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	seedBudgetSpend(t, 100, 100)
	agentID := createHandlerTestAgent(t, "budget-warn-chat-agent", []byte("[]"))
	sessionID := createHandlerTestChatSession(t, agentID)

	previous := testHandler.TaskService.FeatureFlags
	provider := featureflag.NewStaticProvider()
	provider.Set(featureflags.RuntimeCostBudgetWarnOnly, featureflag.Rule{Default: true})
	testHandler.TaskService.FeatureFlags = featureflag.NewService(provider)
	t.Cleanup(func() { testHandler.TaskService.FeatureFlags = previous })

	w := sendChatMessage(t, sessionID, "warn-only send")

	if w.Code < 200 || w.Code > 299 {
		t.Fatalf("warn-only send: expected 2xx, got %d: %s", w.Code, w.Body.String())
	}
	if got := countSessionRows(t, `SELECT count(*) FROM chat_message WHERE chat_session_id = $1`, sessionID); got != 1 {
		t.Errorf("warn-only send persisted %d chat messages, want 1", got)
	}
	if got := countSessionRows(t, `SELECT count(*) FROM agent_task_queue WHERE chat_session_id = $1`, sessionID); got != 1 {
		t.Errorf("warn-only send persisted %d tasks, want 1", got)
	}
	var body, details string
	// Newest first: earlier tests in this suite process leave their own
	// budget notices behind (the handler fixture user outlives them and
	// inbox_item has no cascade), and only this test's row is under assert.
	if err := testPool.QueryRow(context.Background(),
		`SELECT body, details FROM inbox_item WHERE type = 'runtime_budget_exceeded' ORDER BY created_at DESC LIMIT 1`).Scan(&body, &details); err != nil {
		t.Fatalf("warn-only notice missing from inbox: %v", err)
	}
	if !strings.Contains(body, "warn-only") || !strings.Contains(body, "still allowed") {
		t.Errorf("warn-only notice body must say runs are allowed, got %q", body)
	}
	if !strings.Contains(strings.ReplaceAll(details, " ", ""), `"warn_only":"true"`) {
		t.Errorf("warn-only notice details must carry the marker, got %s", details)
	}
}

// TestPutRuntimeCostBudget_OwnerOnly locks in the permission gate: the budget
// caps what the runtime owner's own machine spends, so a workspace owner gets
// 403 exactly like any other non-owner, and the runtime owner gets 200.
func TestPutRuntimeCostBudget_OwnerOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ownerID := seedSecurityTestOwner(t, "budget-runtime-owner")
	runtimeID := dbfx.Runtime(t, "budget-owner-runtime", testutil.Cols{"owner_id": ownerID})

	put := func(asUserID string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequestAs(asUserID, "PUT", "/api/runtimes/"+runtimeID+"/budget", map[string]any{
			"runtime": map[string]any{"daily_usd": 10},
		})
		req = withURLParam(req, "runtimeId", runtimeID)
		testHandler.PutRuntimeCostBudget(w, req)
		return w
	}

	w := put(testUserID)
	if w.Code != http.StatusForbidden {
		t.Fatalf("PUT by workspace owner (not runtime owner): expected 403, got %d: %s", w.Code, w.Body.String())
	}

	w = put(ownerID)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT by runtime owner: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Runtime *struct {
			Daily *struct {
				LimitUSD float64 `json:"limit_usd"`
			} `json:"daily"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode budget response: %v (body=%s)", err, w.Body.String())
	}
	if resp.Runtime == nil || resp.Runtime.Daily == nil || resp.Runtime.Daily.LimitUSD != 10 {
		t.Fatalf("budget response = %s, want runtime daily limit 10", w.Body.String())
	}
	var ticks int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT daily_limit_usd_ticks FROM runtime_cost_budget WHERE runtime_id = $1 AND user_id IS NULL`,
		util.MustParseUUID(runtimeID)).Scan(&ticks); err != nil {
		t.Fatalf("budget row not persisted: %v", err)
	}
	if ticks != 100_000_000_000 { // $10 in 1e-10 USD ticks
		t.Fatalf("persisted ticks = %d, want 100_000_000_000", ticks)
	}
}
