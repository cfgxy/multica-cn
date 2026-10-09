package handler

// Tests for the retrospective config surface (RUYI-552 direction 3): the UI
// picks one workspace agent as the retrospective executor, enabling requires
// a runnable agent (exists in this workspace, not archived, has a runtime),
// and the manual trigger enqueues exactly one no-issue platform task through
// the configured agent — never an issue and never a comment.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/retrospective"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func retroConfigCall(t *testing.T, method string, body any, wsID, userID string) (int, string) {
	t.Helper()
	var h http.HandlerFunc
	switch method {
	case http.MethodGet:
		h = testHandler.GetRetrospectiveConfig
	case http.MethodPut:
		h = testHandler.UpdateRetrospectiveConfig
	case http.MethodPost:
		h = testHandler.TriggerRetrospectiveRun
	}
	return legislationCall(t, h, legislationReq(userID, wsID, method, "/api/retrospective/config", body))
}

type retroConfigStatus struct {
	Enabled         bool   `json:"enabled"`
	IncludeInReview bool   `json:"include_in_review"`
	WindowDays      int    `json:"window_days"`
	AgentID         string `json:"agent_id"`
	AgentName       string `json:"agent_name"`
}

func getRetroConfig(t *testing.T, wsID, ownerID string) retroConfigStatus {
	t.Helper()
	code, body := retroConfigCall(t, http.MethodGet, nil, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("GET config: expected 200, got %d: %s", code, body)
	}
	var cfg retroConfigStatus
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("GET config body: %v", err)
	}
	return cfg
}

// useRetrospectiveRunner swaps in a Runner over the test pool whose enqueuer
// records every call instead of touching the task service, and returns the
// call log. Restored on cleanup.
func useRetrospectiveRunner(t *testing.T) *[]retrospective.EnqueueParams {
	t.Helper()
	calls := &[]retrospective.EnqueueParams{}
	old := testHandler.RetrospectiveRunner
	testHandler.RetrospectiveRunner = &retrospective.Runner{
		DB:      testPool,
		Queries: db.New(testPool),
		Enqueue: func(ctx context.Context, params retrospective.EnqueueParams) (string, error) {
			*calls = append(*calls, params)
			return "f47ac10b-58cc-4372-a567-0e02b2c3d479", nil
		},
	}
	t.Cleanup(func() { testHandler.RetrospectiveRunner = old })
	return calls
}

// retroAgent inserts one runnable agent (with an online runtime) in wsID.
func retroAgent(t *testing.T, wsID, name string) string {
	t.Helper()
	rt := dbfx.Runtime(t, name+" runtime", testutil.Cols{"workspace_id": wsID})
	return dbfx.Agent(t, name, rt, testutil.Cols{"workspace_id": wsID})
}

// TestRetrospectiveConfigAgentRoundTrip: save → read returns the same agent
// selection (id + best-effort display name), and disabling is required
// before the agent can be cleared.
func TestRetrospectiveConfigAgentRoundTrip(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	agentID := retroAgent(t, wsID, "retro executor")

	code, body := retroConfigCall(t, http.MethodPut, map[string]any{
		"enabled":     true,
		"window_days": 3,
		"agent_id":    agentID,
	}, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("PUT config: expected 200, got %d: %s", code, body)
	}
	cfg := getRetroConfig(t, wsID, ownerID)
	if !cfg.Enabled || cfg.AgentID != agentID || cfg.AgentName != "retro executor" {
		t.Fatalf("round-trip mismatch: %+v", cfg)
	}
	if cfg.WindowDays != 3 {
		t.Fatalf("window_days not saved: %+v", cfg)
	}

	// Clearing the agent while enabled must be refused.
	code, body = retroConfigCall(t, http.MethodPut, map[string]any{
		"agent_id": "",
	}, wsID, ownerID)
	if code != http.StatusBadRequest {
		t.Fatalf("clear agent while enabled: expected 400, got %d: %s", code, body)
	}

	// Disable first, then clearing works.
	code, body = retroConfigCall(t, http.MethodPut, map[string]any{
		"enabled":  false,
		"agent_id": "",
	}, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("disable+clear: expected 200, got %d: %s", code, body)
	}
	cfg = getRetroConfig(t, wsID, ownerID)
	if cfg.Enabled || cfg.AgentID != "" {
		t.Fatalf("clear did not persist: %+v", cfg)
	}
}

// TestRetrospectiveConfigAgentValidation: only a runnable agent of this
// workspace is accepted.
func TestRetrospectiveConfigAgentValidation(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)

	otherWs, otherOwner, _ := legislationFixture(t)
	foreignAgent := retroAgent(t, otherWs, "other ws agent")
	_ = otherOwner
	archived := dbfx.Agent(t, "archived agent", "", testutil.Cols{
		"workspace_id": wsID,
		"archived_at":  testutil.Raw("now()"),
	})
	noRuntime := dbfx.Agent(t, "no runtime agent", "", testutil.Cols{"workspace_id": wsID})

	cases := []struct {
		name    string
		agentID string
	}{
		{"agent from another workspace", foreignAgent},
		{"archived agent", archived},
		{"agent without runtime", noRuntime},
		{"not a uuid", "not-a-uuid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := retroConfigCall(t, http.MethodPut, map[string]any{
				"enabled":  true,
				"agent_id": tc.agentID,
			}, wsID, ownerID)
			if code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", code, body)
			}
		})
	}
}

// TestRetrospectiveConfigEnableRequiresAgent: the invariant the scheduler
// relies on — an enabled config always names an agent.
func TestRetrospectiveConfigEnableRequiresAgent(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)

	code, body := retroConfigCall(t, http.MethodPut, map[string]any{
		"enabled": true,
	}, wsID, ownerID)
	if code != http.StatusBadRequest {
		t.Fatalf("enable without agent: expected 400, got %d: %s", code, body)
	}
}

// TestTriggerRetrospectiveRunNoConfig: an unconfigured workspace gets 400.
func TestTriggerRetrospectiveRunNoConfig(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveRunner(t)

	code, body := retroConfigCall(t, http.MethodPost, nil, wsID, ownerID)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", code, body)
	}
}

// TestTriggerRetrospectiveRunAgentNotConfigured: a legacy row (enabled with
// no agent — possible only before migration 935) surfaces the
// agent_not_configured code the UI renders its selector entry from.
func TestTriggerRetrospectiveRunAgentNotConfigured(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	useRetrospectiveRunner(t)
	dbfx.Exec(t, `INSERT INTO retrospective_config (workspace_id, enabled, include_in_review, window_days) VALUES ($1, true, false, 7)`, wsID)

	code, body := retroConfigCall(t, http.MethodPost, nil, wsID, ownerID)
	if code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", code, body)
	}
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Code != "agent_not_configured" {
		t.Fatalf("409 body must carry code agent_not_configured, got %s (%v)", body, err)
	}
}

// TestTriggerRetrospectiveRunEnqueuesExactlyOneAgentTask: the manual trigger
// records the scanned window on the run row, hands exactly one enqueue to
// the platform task path with the configured agent, and creates zero issues
// and zero comments — the run's whole contract.
func TestTriggerRetrospectiveRunEnqueuesExactlyOneAgentTask(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	agentID := retroAgent(t, wsID, "trigger executor")
	calls := useRetrospectiveRunner(t)

	// One completed issue inside the window gives the scan something to
	// hand the agent; without it the run takes the empty-window fast path.
	dbfx.Exec(t, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		VALUES ($1, '复盘素材', 'done', 'none', 'member', $2,
			(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1), 0)`,
		wsID, ownerID)

	issuesBefore := dbfx.Count(t, `SELECT count(*) FROM issue WHERE workspace_id = $1`, wsID)
	commentsBefore := dbfx.Count(t, `SELECT count(*) FROM comment WHERE workspace_id = $1`, wsID)

	code, body := retroConfigCall(t, http.MethodPut, map[string]any{
		"enabled":  true,
		"agent_id": agentID,
	}, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("PUT config: expected 200, got %d: %s", code, body)
	}

	code, body = retroConfigCall(t, http.MethodPost, nil, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("POST run: expected 200, got %d: %s", code, body)
	}
	if n := len(*calls); n != 1 {
		t.Fatalf("expected exactly one enqueue, got %d", n)
	}
	if (*calls)[0].AgentID != agentID {
		t.Fatalf("enqueue agent mismatch: %+v", (*calls)[0])
	}
	if (*calls)[0].RunID == "" || (*calls)[0].WorkspaceID != wsID {
		t.Fatalf("enqueue run/workspace mismatch: %+v", (*calls)[0])
	}

	issuesAfter := dbfx.Count(t, `SELECT count(*) FROM issue WHERE workspace_id = $1`, wsID)
	commentsAfter := dbfx.Count(t, `SELECT count(*) FROM comment WHERE workspace_id = $1`, wsID)
	if issuesAfter != issuesBefore || commentsAfter != commentsBefore {
		t.Fatalf("run touched the issue surface: issues %d→%d comments %d→%d",
			issuesBefore, issuesAfter, commentsBefore, commentsAfter)
	}

	var status, detail string
	dbfx.QueryRow(t, `SELECT status, detail::text FROM retrospective_run WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, wsID).Scan(&status, &detail)
	if status != "running" {
		t.Fatalf("run should stay running until the agent task completes, got %q", status)
	}
	if !strings.Contains(detail, "issue_ids") {
		t.Fatalf("run detail must record the scanned membership, got %s", detail)
	}
}

// TestTriggerRetrospectiveRunEmptyWindowFinishesImmediately: with no
// completed issues in the window the run succeeds without spending an agent
// run — no enqueue at all.
func TestTriggerRetrospectiveRunEmptyWindowFinishesImmediately(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	retroAgent(t, wsID, "quiet executor")
	calls := useRetrospectiveRunner(t)

	code, _ := retroConfigCall(t, http.MethodPut, map[string]any{
		"enabled":  true,
		"agent_id": retroAgent(t, wsID, "quiet executor 2"),
	}, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("PUT config: expected 200")
	}
	code, body := retroConfigCall(t, http.MethodPost, nil, wsID, ownerID)
	if code != http.StatusOK {
		t.Fatalf("POST run: expected 200, got %d: %s", code, body)
	}
	if n := len(*calls); n != 0 {
		t.Fatalf("empty window must not enqueue, got %d calls", n)
	}
	var status string
	dbfx.QueryRow(t, `SELECT status FROM retrospective_run WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, wsID).Scan(&status)
	if status != "succeeded" {
		t.Fatalf("empty-window run should succeed immediately, got %q", status)
	}
}
