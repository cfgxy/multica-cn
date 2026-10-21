package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestClaimTaskByRuntime_CapabilityAdaptationIsOptIn(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	claim := func(label string, contextJSON string) map[string]any {
		t.Helper()
		ctx := context.Background()
		runtimeID := createClaimReclaimRuntime(t, ctx, label+" runtime")
		agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, label+" agent")
		taskID := createDispatchedClaimFixtureTask(t, ctx, agentID, runtimeID, issueID, "120 seconds", false)
		if contextJSON != "" {
			dbfx.Exec(t, `UPDATE agent_task_queue SET context = $2::jsonb WHERE id = $1`, taskID, contextJSON)
		}
		req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, "task-adaptation-claim")
		req = withURLParam(req, "runtimeId", runtimeID)
		w := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
		var response struct {
			Task map[string]any `json:"task"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode claim response: %v", err)
		}
		if response.Task == nil {
			t.Fatalf("claim response has no task: %s", w.Body.String())
		}
		return response.Task
	}

	defaultTask := claim("adaptation disabled", "")
	if _, present := defaultTask["capability_adaptation"]; present {
		t.Fatalf("default claim unexpectedly includes capability adaptation: %#v", defaultTask["capability_adaptation"])
	}

	configured := `{"capability_adaptation":{"policy_id":"policy-claim","policy_version":"v1","scope":"current_task","trigger":"task shape","current_strategy":"long plan","suggested_strategy":"short checkpoints","allowed_actions":["use checkpoints"]}}`
	configuredTask := claim("adaptation enabled", configured)
	payload, ok := configuredTask["capability_adaptation"].(map[string]any)
	if !ok || payload["policy_id"] != "policy-claim" || payload["scope"] != "current_task" {
		t.Fatalf("claim capability_adaptation = %#v", configuredTask["capability_adaptation"])
	}
}
