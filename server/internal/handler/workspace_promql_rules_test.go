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

	rulesRequest := map[string]any{"rules": []any{validWorkspacePromQLRule()}}
	var saved workspacePromQLRulesResponse
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, ownerID, rulesRequest),
	).Want(http.StatusOK).JSON(&saved)
	if len(saved.Rules) != 1 || saved.Rules[0].State.Firing {
		t.Fatalf("owner save returned unexpected rules: %+v", saved.Rules)
	}

	dbfx.Exec(t, `UPDATE workspace SET settings = jsonb_set(settings, '{promql_rules,rules,0,state}', '{"firing":true}'::jsonb) WHERE id = $1`, workspaceID)
	renamedRule := validWorkspacePromQLRule()
	renamedRule["name"] = "Task failures renamed"
	var renamed workspacePromQLRulesResponse
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, ownerID, map[string]any{"rules": []any{renamedRule}}),
	).Want(http.StatusOK).JSON(&renamed)
	if len(renamed.Rules) != 1 || !renamed.Rules[0].State.Firing || renamed.Rules[0].State.LastEvaluatedAt != nil {
		t.Fatalf("editing a firing rule must retain firing state and schedule immediate evaluation: %+v", renamed.Rules)
	}

	disabledRule := validWorkspacePromQLRule()
	disabledRule["name"] = "Task failures renamed"
	disabledRule["enabled"] = false
	var disabled workspacePromQLRulesResponse
	testutil.Call(t, testHandler.PutWorkspacePromQLRules,
		workspacePromQLRequest(http.MethodPut, workspaceID, ownerID, map[string]any{"rules": []any{disabledRule}}),
	).Want(http.StatusOK).JSON(&disabled)
	if len(disabled.Rules) != 1 || disabled.Rules[0].State.Firing {
		t.Fatalf("disabling a firing rule must clear its stored state: %+v", disabled.Rules)
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
	if stored.Unrelated != "preserved" || len(stored.PromQL.Rules) != 1 {
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
