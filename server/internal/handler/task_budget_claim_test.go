package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/taskbudget"
)

func TestClaimUsesFrozenBudgetProfile(t *testing.T) {
	ctx := context.Background()
	for _, frozen := range []bool{true, false} {
		t.Run(map[bool]string{true: "frozen", false: "legacy"}[frozen], func(t *testing.T) {
			runtimeID := createClaimReclaimRuntime(t, ctx, "budget claim")
			agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "budget claim agent")
			dbfx.Exec(t, `UPDATE agent SET runtime_config='{"max_turns":5,"budget_profile":{"max_runs":100}}' WHERE id=$1`, agentID)
			cols := testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID}
			if frozen {
				cols["context"] = testutil.Raw(`'{"budget_scope":{"scope_id":"frozen-test","profile":{"max_runs":2,"per_run_timeout_seconds":9}},"other":"keep"}'::jsonb`)
			}
			taskID := dbfx.Task(t, agentID, cols)
			resp := claimRuntimeManifest(t, testHandler, runtimeID, false)
			profile, err := taskbudget.ParseRuntimeConfig(resp.Agent.RuntimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			if frozen && (profile == nil || profile.MaxRuns != 2 || profile.PerRunTimeoutSeconds != 9) {
				t.Fatalf("claim reset frozen profile: %s", resp.Agent.RuntimeConfig)
			}
			if !frozen && profile != nil {
				t.Fatalf("legacy task adopted a new profile: %s", resp.Agent.RuntimeConfig)
			}
			var config map[string]json.RawMessage
			if err := json.Unmarshal(resp.Agent.RuntimeConfig, &config); err != nil || string(config["max_turns"]) != "5" {
				t.Fatalf("other config lost: %s (%v)", resp.Agent.RuntimeConfig, err)
			}
			if frozen {
				_, raw := storedRuntimeManifest(t, taskID)
				if _, err := taskbudget.FromContext(raw); err != nil {
					t.Fatalf("manifest damaged budget snapshot: %v", err)
				}
			}
		})
	}
}
