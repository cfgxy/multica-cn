package handler

import (
	"encoding/json"
	"net/http"
	"testing"

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
		Audit    []struct {
			Version int `json:"version"`
		} `json:"audit"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !got.Enabled || got.AgentID != governanceAgent || got.Cadence != "weekly" || got.Timezone != "Asia/Shanghai" || len(got.Audit) != 1 {
		t.Fatalf("governance config or audit mismatch: %+v", got)
	}
	var preservedAgent string
	var preservedEnabled bool
	dbfx.QueryRow(t, `SELECT agent_id::text, enabled FROM retrospective_config WHERE workspace_id = $1`, wsID).Scan(&preservedAgent, &preservedEnabled)
	if preservedAgent != retroExecutor || !preservedEnabled {
		t.Fatalf("retrospective config was changed: agent=%q enabled=%t", preservedAgent, preservedEnabled)
	}

	code, body = governanceConfigCall(t, governanceConfigRoute(testHandler.UpdateKnowledgeGovernanceConfig).ServeHTTP, memberID, wsID, map[string]any{"enabled": false})
	if code != http.StatusForbidden {
		t.Fatalf("ordinary member update: expected 403, got %d: %s", code, body)
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
