package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAgentUpdateRejectsInvalidBudgetProfile(t *testing.T) {
	id := staticPromptCarrier(t, "agent")
	for _, profile := range []any{
		map[string]any{"max_runs": -1},
		map[string]any{"max_total_tokens": "100"},
		map[string]any{"max_cost_usd_ticks": nil},
		map[string]any{"unknown_limit": 4},
	} {
		req := withURLParams(newRequest(http.MethodPatch, "/api/agents/"+id+"?workspace_id="+testWorkspaceID,
			map[string]any{"runtime_config": map[string]any{"budget_profile": profile}}), "id", id)
		testutil.Call(t, testHandler.UpdateAgent, req).Want(http.StatusBadRequest)
	}
	var configured bool
	dbfx.QueryRow(t, `SELECT runtime_config ? 'budget_profile' FROM agent WHERE id=$1`, id).Scan(&configured)
	if configured {
		t.Fatal("invalid budget profile was persisted")
	}
}
