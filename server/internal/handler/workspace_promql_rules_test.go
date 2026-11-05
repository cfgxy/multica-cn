package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/promquery"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func workspacePromQLRequest(method, workspaceID, userID string, body any) *http.Request {
	req := testutil.JSONRequest(method, "/api/workspace/promql-rules", body)
	return testutil.WithHeaders(req,
		"X-Workspace-ID", workspaceID,
		"X-User-ID", userID,
	)
}

func validWorkspacePromQLRule() map[string]any {
	return map[string]any{
		"id":                 "8da35391-fd75-4b80-b4ec-436d563c5348",
		"name":               "Task failures",
		"enabled":            true,
		"metric":             "failures",
		"grouping":           "workspace",
		"condition":          "threshold",
		"comparison":         ">=",
		"threshold":          5,
		"recovery_threshold": 3,
		"lookback_seconds":   300,
		"evaluation_seconds": 30,
		"for_seconds":        0,
		"no_data_policy":     "keep",
		"query_error_policy": "keep",
	}
}

func TestWorkspacePromQLRules_RoleGatesPersistenceAndPreview(t *testing.T) {
	if testHandler == nil || testPool == nil || dbfx == nil {
		t.Skip("database not available")
	}

	workspaceID := dbfx.Workspace(t, "PromQL rules", handlerTestSlug("promql-rules"))
	dbfx.Cleanup(t, `DELETE FROM audit_event WHERE workspace_id = $1`, workspaceID)
	ownerID := dbfx.User(t, "PromQL owner "+handlerFixtureSuffix, "promql-owner+"+handlerFixtureSuffix+"@multica.test")
	memberID := dbfx.User(t, "PromQL member "+handlerFixtureSuffix, "promql-member+"+handlerFixtureSuffix+"@multica.test")
	adminID := dbfx.User(t, "PromQL admin "+handlerFixtureSuffix, "promql-admin+"+handlerFixtureSuffix+"@multica.test")
	dbfx.Member(t, workspaceID, ownerID, "owner")
	dbfx.Member(t, workspaceID, memberID, "member")
	dbfx.Member(t, workspaceID, adminID, "admin")
	dbfx.Exec(t, `UPDATE workspace SET settings = '{"unrelated":"preserved"}'::jsonb WHERE id = $1`, workspaceID)

	testutil.Call(t, testHandler.GetWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodGet, workspaceID, memberID, nil),
	).Want(http.StatusOK)

	memberRules := map[string]any{"rules": []any{validWorkspacePromQLRule()}}
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, memberID, memberRules),
	).Want(http.StatusForbidden)

	primaryRule := validWorkspacePromQLRule()
	siblingRule := validWorkspacePromQLRule()
	siblingRule["id"] = "ad9d1473-7c2a-47b1-a2fa-59efab1130fa"
	siblingRule["name"] = "Runtime errors"
	rulesRequest := map[string]any{"rules": []any{primaryRule, siblingRule}}
	var saved workspacePromQLRulesResponse
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, ownerID, rulesRequest),
	).Want(http.StatusOK).JSON(&saved)
	if len(saved.Rules) != 2 || saved.Rules[0].State.Firing {
		t.Fatalf("owner save returned unexpected rules: %+v", saved.Rules)
	}

	runtimeID := dbfx.Runtime(t, "PromQL rules runtime", testutil.Cols{
		"workspace_id": workspaceID,
		"owner_id":     ownerID,
	})
	agentID := dbfx.Agent(t, "PromQL rules agent", runtimeID, testutil.Cols{
		"workspace_id": workspaceID,
		"owner_id":     ownerID,
		"visibility":   "workspace",
	})
	insertRulePause := func(ruleID string) {
		t.Helper()
		dbfx.InsertNoID(t, "agent_promql_rule_pause", testutil.Cols{
			"workspace_id": workspaceID,
			"agent_id":     agentID,
			"rule_id":      ruleID,
			"rule_name":    "test rule",
			"reason":       "rule_firing",
		}, "workspace_id = $1 AND agent_id = $2 AND rule_id = $3", workspaceID, agentID, ruleID)
	}
	primaryRuleID := primaryRule["id"].(string)
	siblingRuleID := siblingRule["id"].(string)
	dbfx.Exec(t, `UPDATE workspace SET settings = jsonb_set(settings, '{promql_rules,rules,0,state}', '{"firing":true}'::jsonb) WHERE id = $1`, workspaceID)
	insertRulePause(primaryRuleID)
	insertRulePause(siblingRuleID)
	renamedRule := validWorkspacePromQLRule()
	renamedRule["name"] = "Task failures renamed"
	var reconfigured workspacePromQLRulesResponse
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, ownerID, map[string]any{"rules": []any{renamedRule, siblingRule}}),
	).Want(http.StatusOK).JSON(&reconfigured)
	if len(reconfigured.Rules) != 2 || reconfigured.Rules[0].State.Firing || reconfigured.Rules[0].AgentStates != nil {
		t.Fatalf("reconfiguring a firing rule must clear its runtime state: %+v", reconfigured.Rules)
	}
	var primaryPauses, siblingPauses int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE rule_id = $2), count(*) FILTER (WHERE rule_id = $3)
		FROM agent_promql_rule_pause WHERE workspace_id = $1 AND agent_id = $4
	`, workspaceID, primaryRuleID, siblingRuleID, agentID).Scan(&primaryPauses, &siblingPauses); err != nil {
		t.Fatalf("count reconfigured rule pauses: %v", err)
	}
	if primaryPauses != 0 || siblingPauses != 1 {
		t.Fatalf("reconfigure paused sources = primary:%d sibling:%d; want primary:0 sibling:1", primaryPauses, siblingPauses)
	}
	var reconfiguredHistory int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_suppression_event
		WHERE workspace_id = $1 AND agent_id = $2 AND source = 'promql_rule'
		  AND rule_id = $3 AND event_type = 'cleared' AND reason = 'rule_reconfigured'
	`, workspaceID, agentID, primaryRuleID).Scan(&reconfiguredHistory); err != nil {
		t.Fatalf("read reconfigured source history: %v", err)
	}
	if reconfiguredHistory != 1 {
		t.Fatalf("reconfigured source clear history = %d, want 1", reconfiguredHistory)
	}

	dbfx.Exec(t, `UPDATE workspace SET settings = jsonb_set(settings, '{promql_rules,rules,0,state}', '{"firing":true}'::jsonb) WHERE id = $1`, workspaceID)
	insertRulePause(primaryRuleID)
	disabledRule := renamedRule
	disabledRule["enabled"] = false
	var disabled workspacePromQLRulesResponse
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, ownerID, map[string]any{"rules": []any{disabledRule, siblingRule}}),
	).Want(http.StatusOK).JSON(&disabled)
	if len(disabled.Rules) != 2 || disabled.Rules[0].State.Firing {
		t.Fatalf("disabling a firing rule must clear its stored state: %+v", disabled.Rules)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE rule_id = $2), count(*) FILTER (WHERE rule_id = $3)
		FROM agent_promql_rule_pause WHERE workspace_id = $1 AND agent_id = $4
	`, workspaceID, primaryRuleID, siblingRuleID, agentID).Scan(&primaryPauses, &siblingPauses); err != nil {
		t.Fatalf("count disabled rule pauses: %v", err)
	}
	if primaryPauses != 0 || siblingPauses != 1 {
		t.Fatalf("disable paused sources = primary:%d sibling:%d; want primary:0 sibling:1", primaryPauses, siblingPauses)
	}
	var resolvedCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE workspace_id = $1 AND event_type = 'ops.promql_rule_resolved' AND details->>'resolution_cause' = 'rule_disabled'`, workspaceID).Scan(&resolvedCount); err != nil {
		t.Fatalf("read rule resolution audit: %v", err)
	}
	if resolvedCount != 1 {
		t.Fatalf("disabling a firing rule must append one resolution audit, got %d", resolvedCount)
	}

	// Admins are authorized for the same write surface as owners.
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, adminID, rulesRequest),
	).Want(http.StatusOK)

	// Missing rules and trailing JSON must not be interpreted as a request to
	// delete the existing configuration.
	for _, body := range []string{"{}", `{"rules":[]} {"rules":[]}`} {
		testutil.Call(t, testHandler.PutWorkspacePromQLRules,
			workspacePromQLRequest(http.MethodPut, workspaceID, ownerID, body),
		).Want(http.StatusBadRequest)
	}

	var settings []byte
	if err := testPool.QueryRow(context.Background(), `SELECT settings FROM workspace WHERE id = $1`, workspaceID).Scan(&settings); err != nil {
		t.Fatalf("read saved settings: %v", err)
	}
	var stored struct {
		Unrelated string `json:"unrelated"`
		PromQL    struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"promql_rules"`
	}
	if err := json.Unmarshal(settings, &stored); err != nil {
		t.Fatalf("decode workspace settings: %v", err)
	}
	if stored.Unrelated != "preserved" || len(stored.PromQL.Rules) != 2 {
		t.Fatalf("settings update lost unrelated configuration or rules: %s", settings)
	}

	var askedQuery string
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		askedQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1760000000,"0"]}]}}`))
	}))
	t.Cleanup(promServer.Close)
	previousPrometheus := testHandler.Prometheus
	testHandler.Prometheus = promquery.NewClient(promServer.URL)
	t.Cleanup(func() { testHandler.Prometheus = previousPrometheus })

	var preview workspacePromQLPreviewResponse
	testutil.Call(t, testHandler.PreviewWorkspacePromQLRule,
		workspacePromQLRequest(http.MethodPost, workspaceID, adminID, map[string]any{"rule": validWorkspacePromQLRule()}),
	).Want(http.StatusOK).JSON(&preview)
	if preview.Matches {
		t.Fatal("nonmatching zero-duration preview must not report a match")
	}
	if !strings.Contains(askedQuery, "multica_agent_task_failures_total") || !strings.Contains(askedQuery, workspaceID) {
		t.Fatalf("preview query must use the fixed metric and workspace selector, got %q", askedQuery)
	}
}

func TestPutWorkspacePromQLRules_DeletesPolicySelections(t *testing.T) {
	if testHandler == nil || testPool == nil || dbfx == nil {
		t.Skip("database not available")
	}

	workspaceID, agentID := backpressurePromQLFixture(t)
	firstRule := validWorkspacePromQLRule()
	secondRule := validWorkspacePromQLRule()
	secondRule["id"] = "ad9d1473-7c2a-47b1-a2fa-59efab1130fa"
	secondRule["name"] = "Runtime errors"
	firstRuleID := firstRule["id"].(string)
	secondRuleID := secondRule["id"].(string)
	storeWorkspacePromQLRules(t, workspaceID, []map[string]any{firstRule, secondRule})

	policy := validBPBody()
	policy["promql_rule_ids"] = []string{firstRuleID, secondRuleID}
	if w := agentBackpressurePolicyPut(t, workspaceID, agentID, policy); w.Code != http.StatusOK {
		t.Fatalf("save selected rules: got %d -- %s", w.Code, w.Body.String())
	}
	selectedIDs := func() string {
		t.Helper()
		var ids string
		if err := testPool.QueryRow(context.Background(), `
			SELECT array_to_string(promql_rule_ids, ',')
			FROM agent_backpressure_policy
			WHERE workspace_id = $1 AND agent_id = $2
		`, workspaceID, agentID).Scan(&ids); err != nil {
			t.Fatalf("read selected rules: %v", err)
		}
		return ids
	}

	cloneRule := func(rule map[string]any) map[string]any {
		clone := make(map[string]any, len(rule))
		for key, value := range rule {
			clone[key] = value
		}
		return clone
	}
	disabledRule := cloneRule(firstRule)
	disabledRule["enabled"] = false
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, testUserID, map[string]any{"rules": []any{disabledRule, secondRule}}),
	).Want(http.StatusOK)
	if got, want := selectedIDs(), firstRuleID+","+secondRuleID; got != want {
		t.Fatalf("disabling a rule changed selections: got %q, want %q", got, want)
	}

	reconfiguredRule := cloneRule(firstRule)
	reconfiguredRule["name"] = "Task failures reconfigured"
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, testUserID, map[string]any{"rules": []any{reconfiguredRule, secondRule}}),
	).Want(http.StatusOK)
	if got, want := selectedIDs(), firstRuleID+","+secondRuleID; got != want {
		t.Fatalf("reconfiguring a rule changed selections: got %q, want %q", got, want)
	}

	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, testUserID, map[string]any{"rules": []any{secondRule}}),
	).Want(http.StatusOK)
	if got := selectedIDs(); got != secondRuleID {
		t.Fatalf("deleting one rule retained stale or removed sibling selections: got %q, want %q", got, secondRuleID)
	}

	updatedPolicy := validBPBody()
	updatedPolicy["window_minutes"] = 45
	updatedPolicy["promql_rule_ids"] = []string{secondRuleID}
	if w := agentBackpressurePolicyPut(t, workspaceID, agentID, updatedPolicy); w.Code != http.StatusOK {
		t.Fatalf("save policy after rule deletion: got %d -- %s", w.Code, w.Body.String())
	}
}

func TestGetWorkspacePromQLRules_FiltersPrivateAgentGroupedState(t *testing.T) {
	if testHandler == nil || testPool == nil || dbfx == nil {
		t.Skip("database not available")
	}

	workspaceID := dbfx.Workspace(t, "PromQL state visibility", handlerTestSlug("promql-state-visibility"))
	dbfx.Member(t, workspaceID, testUserID, "owner")
	memberID := dbfx.User(t, "PromQL state member "+handlerFixtureSuffix, "promql-state-member+"+handlerFixtureSuffix+"@multica.test")
	privateOwnerID := dbfx.User(t, "PromQL private owner "+handlerFixtureSuffix, "promql-private-owner+"+handlerFixtureSuffix+"@multica.test")
	dbfx.Member(t, workspaceID, memberID, "member")
	dbfx.Member(t, workspaceID, privateOwnerID, "member")
	runtimeID := dbfx.Runtime(t, "PromQL state runtime", testutil.Cols{
		"workspace_id": workspaceID,
		"owner_id":     testUserID,
	})
	privateAgentID := dbfx.Agent(t, "PromQL private state agent", runtimeID, testutil.Cols{
		"workspace_id":    workspaceID,
		"owner_id":        privateOwnerID,
		"visibility":      "private",
		"permission_mode": "private",
	})
	rule := validWorkspacePromQLRule()
	rule["grouping"] = "agent"
	rule["state"] = map[string]any{
		"firing": true,
		"values": []any{map[string]any{
			"labels": map[string]string{"agent_id": privateAgentID},
			"value":  9,
		}},
	}
	rule["agent_states"] = map[string]any{
		privateAgentID: map[string]any{
			"firing": true,
			"values": []any{map[string]any{
				"labels": map[string]string{"agent_id": privateAgentID},
				"value":  9,
			}},
		},
	}
	storeWorkspacePromQLRules(t, workspaceID, []map[string]any{rule})

	var memberRules workspacePromQLRulesResponse
	testutil.Call(t, testHandler.GetWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodGet, workspaceID, memberID, nil),
	).Want(http.StatusOK).JSON(&memberRules)
	if len(memberRules.Rules) != 1 {
		t.Fatalf("member rules = %d, want 1", len(memberRules.Rules))
	}
	memberRule := memberRules.Rules[0]
	if memberRule.State.Firing || len(memberRule.State.Values) != 0 || len(memberRule.AgentStates) != 0 {
		t.Fatalf("member response leaked private agent state: %+v", memberRule)
	}
	memberBody, err := json.Marshal(memberRules)
	if err != nil {
		t.Fatalf("encode member rules: %v", err)
	}
	if strings.Contains(string(memberBody), privateAgentID) {
		t.Fatalf("member response leaked private agent id %s: %s", privateAgentID, memberBody)
	}

	var ownerRules workspacePromQLRulesResponse
	testutil.Call(t, testHandler.GetWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodGet, workspaceID, testUserID, nil),
	).Want(http.StatusOK).JSON(&ownerRules)
	if len(ownerRules.Rules) != 1 || !ownerRules.Rules[0].State.Firing || len(ownerRules.Rules[0].AgentStates) != 1 {
		t.Fatalf("workspace owner lost authorized agent state: %+v", ownerRules.Rules)
	}

	policy := validBPBody()
	policy["promql_rule_ids"] = []string{rule["id"].(string)}
	if w := agentBackpressurePolicyPut(t, workspaceID, privateAgentID, policy); w.Code != http.StatusOK {
		t.Fatalf("save private agent policy: got %d -- %s", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	req := newRequestAs(privateOwnerID, http.MethodGet, "/api/agents/"+privateAgentID+"/backpressure-policy", nil)
	req.Header.Set("X-Workspace-ID", workspaceID)
	testHandler.GetAgentBackpressurePolicy(w, withURLParam(req, "id", privateAgentID))
	if w.Code != http.StatusOK {
		t.Fatalf("private owner detail: got %d -- %s", w.Code, w.Body.String())
	}
	var detail struct {
		PromQLRules []struct {
			ID    string `json:"id"`
			State struct {
				Firing bool `json:"firing"`
				Values []struct {
					Labels map[string]string `json:"labels"`
				} `json:"values"`
			} `json:"state"`
		} `json:"promql_rules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode private owner detail: %v", err)
	}
	if len(detail.PromQLRules) != 1 || detail.PromQLRules[0].ID != rule["id"].(string) || !detail.PromQLRules[0].State.Firing ||
		len(detail.PromQLRules[0].State.Values) != 1 || detail.PromQLRules[0].State.Values[0].Labels["agent_id"] != privateAgentID {
		t.Fatalf("private owner detail lost own rule state: %+v", detail.PromQLRules)
	}
}
