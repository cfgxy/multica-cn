package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func governanceConfigCall(t *testing.T, h http.HandlerFunc, userID, wsID string, body any) (int, string) {
	t.Helper()
	return legislationCall(t, h, legislationReq(userID, wsID, http.MethodPut, "/api/knowledge/governance/config", body))
}

func governanceConfigRoute(h http.HandlerFunc) http.Handler {
	return middleware.RequireWorkspaceRole(testHandler.Queries, "owner", "admin")(h)
}

// The governance scheduler owns its own config row: changing it must not
// mutate the daily retrospective's selected executor or enabled state.
func TestKnowledgeGovernanceConfigIsolatedAndAudited(t *testing.T) {
	wsID, ownerID, memberID := legislationFixture(t)
	defer testPool.Exec(t.Context(), `DELETE FROM knowledge_governance_config_audit WHERE workspace_id = $1`, wsID)
	defer testPool.Exec(t.Context(), `DELETE FROM knowledge_governance_config WHERE workspace_id = $1`, wsID)

	// Pin the service clock: the persisted initial anchor and the immediate
	// response must agree, rather than relying on wall-clock timing.
	originalNow := testHandler.Now
	testHandler.Now = func() time.Time { return time.Date(2026, time.November, 26, 10, 15, 0, 0, time.UTC) }
	t.Cleanup(func() { testHandler.Now = originalNow })

	retroExecutor := retroAgent(t, wsID, "retro executor")
	governanceAgent := retroAgent(t, wsID, "governance executor")
	dbfx.InsertNoID(t, "retrospective_config", testutil.Cols{
		"workspace_id": wsID, "enabled": true, "include_in_review": false, "window_days": 7, "agent_id": retroExecutor,
	}, "workspace_id = $1", wsID)

	code, body := governanceConfigCall(t, governanceConfigRoute(testHandler.UpdateKnowledgeGovernanceConfig).ServeHTTP, ownerID, wsID, map[string]any{
		"enabled": true, "agent_id": governanceAgent, "cadence": "weekly", "timezone": "Asia/Shanghai",
	})
	if code != http.StatusOK {
		t.Fatalf("save governance config: expected 200, got %d: %s", code, body)
	}
	var got struct {
		Enabled  bool   `json:"enabled"`
		AgentID  string `json:"agent_id"`
		Cadence  string `json:"cadence"`
		Timezone string `json:"timezone"`
		Next     string `json:"next_scheduled_at"`
		Audit    []struct {
			Version int `json:"version"`
		} `json:"audit"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !got.Enabled || got.AgentID != governanceAgent || got.Cadence != "weekly" || got.Timezone != "Asia/Shanghai" || got.Next != "2026-11-29T16:00:00.000Z" || len(got.Audit) != 1 {
		t.Fatalf("governance config or audit mismatch: %+v", got)
	}
	var preservedAgent string
	var preservedEnabled bool
	dbfx.QueryRow(t, `SELECT agent_id::text, enabled FROM retrospective_config WHERE workspace_id = $1`, wsID).Scan(&preservedAgent, &preservedEnabled)
	if preservedAgent != retroExecutor || !preservedEnabled {
		t.Fatalf("retrospective config was changed: agent=%q enabled=%t", preservedAgent, preservedEnabled)
	}

	// Once the initial window is in the past, the API reports the following
	// future calendar boundary rather than re-emitting a stale anchor.
	testHandler.Now = func() time.Time { return time.Date(2026, time.November, 30, 17, 0, 0, 0, time.UTC) }
	code, body = legislationCall(t, governanceConfigRoute(testHandler.GetKnowledgeGovernanceConfig).ServeHTTP, legislationReq(ownerID, wsID, http.MethodGet, "/api/knowledge/governance/config", nil))
	if code != http.StatusOK {
		t.Fatalf("read governance config: expected 200, got %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Next != "2026-12-06T16:00:00.000Z" {
		t.Fatalf("post-anchor next schedule = %q, decode err=%v; want 2026-12-06T16:00:00.000Z", got.Next, err)
	}

	code, body = governanceConfigCall(t, governanceConfigRoute(testHandler.UpdateKnowledgeGovernanceConfig).ServeHTTP, memberID, wsID, map[string]any{"enabled": false})
	if code != http.StatusForbidden {
		t.Fatalf("ordinary member update: expected 403, got %d: %s", code, body)
	}
}

func TestKnowledgeGovernanceConfigSerializesConcurrentSaves(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	defer testPool.Exec(t.Context(), `DELETE FROM knowledge_governance_config_audit WHERE workspace_id = $1`, wsID)
	defer testPool.Exec(t.Context(), `DELETE FROM knowledge_governance_config WHERE workspace_id = $1`, wsID)
	agentID := retroAgent(t, wsID, "concurrent governance executor")
	route := governanceConfigRoute(testHandler.UpdateKnowledgeGovernanceConfig).ServeHTTP
	responses := make(chan int, 2)
	for _, cadence := range []string{"daily", "monthly"} {
		go func(cadence string) {
			w := httptest.NewRecorder()
			route(w, legislationReq(ownerID, wsID, http.MethodPut, "/api/knowledge/governance/config", map[string]any{
				"enabled": true, "agent_id": agentID, "cadence": cadence, "timezone": "UTC",
			}))
			responses <- w.Code
		}(cadence)
	}
	for range 2 {
		if code := <-responses; code != http.StatusOK {
			t.Fatalf("concurrent save status = %d, want 200", code)
		}
	}
	var count, minVersion, maxVersion int
	dbfx.QueryRow(t, `
SELECT count(*), min(config_version), max(config_version)
FROM knowledge_governance_config_audit WHERE workspace_id = $1`, wsID).Scan(&count, &minVersion, &maxVersion)
	if count != 2 || minVersion != 1 || maxVersion != 2 {
		t.Fatalf("concurrent saves must allocate distinct serial versions; count=%d range=%d..%d", count, minVersion, maxVersion)
	}
}

func TestKnowledgeGovernanceConfigRejectsForeignAndInvalidExecutor(t *testing.T) {
	wsID, ownerID, _ := legislationFixture(t)
	otherWS, _, _ := legislationFixture(t)
	defer testPool.Exec(t.Context(), `DELETE FROM knowledge_governance_config_audit WHERE workspace_id = $1`, wsID)
	defer testPool.Exec(t.Context(), `DELETE FROM knowledge_governance_config WHERE workspace_id = $1`, wsID)
	foreignAgent := retroAgent(t, otherWS, "foreign governance executor")

	for name, body := range map[string]map[string]any{
		"foreign agent":    {"enabled": true, "agent_id": foreignAgent},
		"missing agent":    {"enabled": true},
		"invalid timezone": {"enabled": false, "timezone": "Mars/Olympus"},
	} {
		t.Run(name, func(t *testing.T) {
			code, response := governanceConfigCall(t, governanceConfigRoute(testHandler.UpdateKnowledgeGovernanceConfig).ServeHTTP, ownerID, wsID, body)
			if code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", code, response)
			}
		})
	}
}
