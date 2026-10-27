package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// Parameter-definition CRUD + trigger-time resolution, end to end through the
// HTTP handlers (RUYI-641 stage A). All tests require a working DB wired in
// TestMain; definition rows are cleaned before the fixture autopilot rows
// because the autopilot_id link is a plain UUID with no cascade.

func cleanupAutopilotParams(t *testing.T, autopilotID string) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM autopilot_param WHERE autopilot_id = $1`, autopilotID)
	})
}

func putAutopilotParams(t *testing.T, autopilotID string, params []service.AutopilotParamInput) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/autopilots/"+autopilotID+"/params", map[string]any{
		"params": params,
	})
	req = withURLParam(req, "id", autopilotID)
	testHandler.ReplaceAutopilotParams(w, req)
	return w
}

func listAutopilotParams(t *testing.T, autopilotID string) AutopilotParamsResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/autopilots/"+autopilotID+"/params", nil)
	req = withURLParam(req, "id", autopilotID)
	testHandler.ListAutopilotParams(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET params: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var listed AutopilotParamsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return listed
}

func cityParam() service.AutopilotParamInput {
	return service.AutopilotParamInput{
		Name:      "city",
		ParamType: "string",
		Required:  true,
	}
}

func TestAutopilotParamsReplaceAndList(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsCRUD Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	cleanupAutopilotParams(t, apID)

	// Empty set first: list answers an empty array, not null.
	if listed := listAutopilotParams(t, apID); listed.Params == nil || len(listed.Params) != 0 {
		t.Fatalf("empty autopilot params = %#v, want empty array", listed.Params)
	}

	// Replace with a three-parameter set; response echoes the set in order.
	set := []service.AutopilotParamInput{
		cityParam(),
		{
			Name:         "retries",
			ParamType:    "number",
			Required:     false,
			DefaultValue: strPtr("3"),
		},
		{
			Name:        "level",
			ParamType:   "enum",
			EnumOptions: []string{"low", "high"},
			Required:    false,
		},
	}
	w := putAutopilotParams(t, apID, set)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT params: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var replaced AutopilotParamsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &replaced); err != nil {
		t.Fatalf("decode replace: %v", err)
	}
	if len(replaced.Params) != 3 {
		t.Fatalf("replaced set length = %d, want 3", len(replaced.Params))
	}
	if replaced.Params[0].Name != "city" || !replaced.Params[0].Required ||
		replaced.Params[0].ParamType != "string" {
		t.Fatalf("param[0] = %#v", replaced.Params[0])
	}
	if replaced.Params[1].DefaultValue == nil || *replaced.Params[1].DefaultValue != "3" {
		t.Fatalf("param[1] default = %#v, want \"3\"", replaced.Params[1].DefaultValue)
	}
	if len(replaced.Params[2].EnumOptions) != 2 || replaced.Params[2].EnumOptions[1] != "high" {
		t.Fatalf("param[2] enum options = %#v", replaced.Params[2].EnumOptions)
	}

	// GET reflects the persisted set.
	if listed := listAutopilotParams(t, apID); len(listed.Params) != 3 || listed.Params[0].Name != "city" {
		t.Fatalf("list after replace = %#v", listed.Params)
	}

	// An empty replacement clears the set.
	if w := putAutopilotParams(t, apID, []service.AutopilotParamInput{}); w.Code != http.StatusOK {
		t.Fatalf("PUT empty params: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if listed := listAutopilotParams(t, apID); len(listed.Params) != 0 {
		t.Fatalf("params after clearing replace = %#v, want empty", listed.Params)
	}
}

func TestAutopilotParamsValidationRejections(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsReject Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	cleanupAutopilotParams(t, apID)

	cases := []struct {
		name   string
		params []service.AutopilotParamInput
		reason string
	}{
		{"bad_name", []service.AutopilotParamInput{{Name: "9bad", ParamType: "string"}}, "name must match"},
		{"duplicate_names", []service.AutopilotParamInput{cityParam(), cityParam()}, "duplicate parameter name"},
		{"bad_type", []service.AutopilotParamInput{{Name: "city", ParamType: "float"}}, "unknown param_type"},
		{"enum_default_outside_options", []service.AutopilotParamInput{{
			Name: "level", ParamType: "enum",
			EnumOptions:  []string{"low", "high"},
			DefaultValue: strPtr("medium"),
		}}, "default_value must be one of the enum options"},
		{"reserved_builtin_name", []service.AutopilotParamInput{{Name: "date", ParamType: "string"}}, "reserved for the built-in"},
	}
	for _, tc := range cases {
		w := putAutopilotParams(t, apID, tc.params)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d body=%s", tc.name, w.Code, w.Body.String())
		}
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if got, _ := resp["error"].(string); !strings.Contains(got, tc.reason) {
			t.Fatalf("%s: error = %q, want substring %q", tc.name, got, tc.reason)
		}
	}

	// A definition set the stored issue-title template still references cannot
	// be removed: the replacement is rejected with the offending token named.
	if w := putAutopilotParams(t, apID, []service.AutopilotParamInput{cityParam()}); w.Code != http.StatusOK {
		t.Fatalf("seed city param: got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE autopilot SET issue_title_template = $1 WHERE id = $2`,
		`Report for {{city}}`, apID); err != nil {
		t.Fatalf("set title template: %v", err)
	}
	w := putAutopilotParams(t, apID, []service.AutopilotParamInput{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("removing referenced param: expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if got, _ := resp["error"].(string); !strings.Contains(got, "city") {
		t.Fatalf("rejection must name the dangling token, got %#v", resp["error"])
	}
}

func TestCreateAutopilotWithParamsAndTemplateTokens(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsCreate Agent")

	// Params + a title template that references them are accepted together.
	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/autopilots", map[string]any{
		"title":                "params create ok",
		"assignee_id":          agentID,
		"execution_mode":       "run_only",
		"issue_title_template": "Digest for {{city}} on {{date}}",
		"params": []map[string]any{
			{"name": "city", "param_type": "string", "required": true},
		},
	})
	testHandler.CreateAutopilot(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAutopilot: expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var ap AutopilotResponse
	if err := json.Unmarshal(w.Body.Bytes(), &ap); err != nil {
		t.Fatalf("decode autopilot: %v", err)
	}
	cleanupAutopilotParams(t, ap.ID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, ap.ID)
	})

	if listed := listAutopilotParams(t, ap.ID); len(listed.Params) != 1 || listed.Params[0].Name != "city" {
		t.Fatalf("created params = %#v, want [city]", listed.Params)
	}

	// An undefined token is rejected at create time.
	w = httptest.NewRecorder()
	r = newRequest("POST", "/api/autopilots", map[string]any{
		"title":                "params create bad token",
		"assignee_id":          agentID,
		"execution_mode":       "run_only",
		"issue_title_template": "Digest for {{nope}}",
	})
	testHandler.CreateAutopilot(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with unknown token: expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTriggerAutopilotParamsRejectAndSnapshot(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsTrigger Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	cleanupAutopilotParams(t, apID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM autopilot_run WHERE autopilot_id = $1`, apID)
	})

	// city: required, no default; verbose: optional boolean with default true.
	set := []service.AutopilotParamInput{
		cityParam(),
		{Name: "verbose", ParamType: "boolean", DefaultValue: strPtr("true")},
	}
	if w := putAutopilotParams(t, apID, set); w.Code != http.StatusOK {
		t.Fatalf("seed params: got %d body=%s", w.Code, w.Body.String())
	}

	trigger := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/autopilots/"+apID+"/trigger", body)
		req = withURLParam(req, "id", apID)
		testHandler.TriggerAutopilot(w, req)
		return w
	}

	// Missing required param → 400 naming the parameter, no run created.
	w := trigger(map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing required: expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var errResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if got, _ := errResp["error"].(string); !strings.Contains(got, "city") {
		t.Fatalf("rejection must name the missing param, got %#v", errResp["error"])
	}

	// Type mismatch → 400; booleans are not accepted as strings.
	w = trigger(map[string]any{"params": map[string]any{"city": "北京", "verbose": "yes"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("type mismatch: expected 400, got %d body=%s", w.Code, w.Body.String())
	}

	// Unknown parameter name → 400.
	w = trigger(map[string]any{"params": map[string]any{"city": "北京", "ghost": "1"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown param: expected 400, got %d body=%s", w.Code, w.Body.String())
	}

	// A valid call resolves and snapshots: supplied city, defaulted verbose.
	w = trigger(map[string]any{"params": map[string]any{"city": "北京"}})
	if w.Code != http.StatusOK {
		t.Fatalf("valid trigger: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var runResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	runID, _ := runResp["id"].(string)
	if runID == "" {
		t.Fatalf("run response missing id: %#v", runResp)
	}
	var payload []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT trigger_payload FROM autopilot_run WHERE id = $1`, runID).Scan(&payload); err != nil {
		t.Fatalf("load run payload: %v", err)
	}
	var snap struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("decode snapshot: %v body=%s", err, string(payload))
	}
	if snap.Params["city"] != "北京" {
		t.Fatalf("snapshot city = %#v, want 北京", snap.Params["city"])
	}
	// The default resolves to JSON true, not the string "true".
	if snap.Params["verbose"] != true {
		t.Fatalf("snapshot verbose = %#v, want boolean true", snap.Params["verbose"])
	}
}

func TestWebhookParamsSkippedOnInvalid(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsWebhook Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	cleanupAutopilotParams(t, apID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM autopilot_run WHERE autopilot_id = $1`, apID)
	})
	trig := createWebhookTriggerViaHandler(t, apID)

	if w := putAutopilotParams(t, apID, []service.AutopilotParamInput{cityParam()}); w.Code != http.StatusOK {
		t.Fatalf("seed params: got %d body=%s", w.Code, w.Body.String())
	}

	// Missing required param: ingress answers 200 skipped with the parameter
	// named, so the provider's retry machinery doesn't hammer a 4xx.
	w := postWebhook(t, *trig.WebhookToken, map[string]any{
		"event":  "demo.received",
		"params": map[string]any{},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook invalid params: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "skipped" {
		t.Fatalf("status = %#v, want skipped", resp["status"])
	}
	if reason, _ := resp["reason"].(string); !strings.Contains(reason, "city") {
		t.Fatalf("skip reason must name the missing param, got %#v", resp["reason"])
	}

	// Valid params: accepted, and the run snapshot carries resolved values.
	w = postWebhook(t, *trig.WebhookToken, map[string]any{
		"event":  "demo.received",
		"params": map[string]any{"city": "上海"},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook valid params: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	resp = nil
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "accepted" {
		t.Fatalf("status = %#v, want accepted", resp["status"])
	}
	runID, _ := resp["run_id"].(string)
	var payload []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT trigger_payload FROM autopilot_run WHERE id = $1`, runID).Scan(&payload); err != nil {
		t.Fatalf("load run payload: %v", err)
	}
	var snap struct {
		Event  string         `json:"event"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("decode snapshot: %v body=%s", err, string(payload))
	}
	if snap.Event != "demo.received" {
		t.Fatalf("envelope event preserved = %q", snap.Event)
	}
	if snap.Params["city"] != "上海" {
		t.Fatalf("snapshot city = %#v, want 上海", snap.Params["city"])
	}
}

// TestAutopilotParamsContractRoundTrip (RUYI-641 stage B) pins the calling
// contract's discoverability: GET /params enumerates every definition with the
// fields a caller needs (name, param_type, required, default_value,
// enum_options), and a /trigger body constructed ONLY from that enumeration —
// required values caller-supplied, everything else coerced from default_value
// through param_type — is accepted and snapshots exactly the constructed set.
// A caller never needs the edit UI to integrate.
func TestAutopilotParamsContractRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsContract Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	cleanupAutopilotParams(t, apID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM autopilot_run WHERE autopilot_id = $1`, apID)
	})

	set := []service.AutopilotParamInput{
		cityParam(), // required string, no default
		{Name: "retries", ParamType: "number", DefaultValue: strPtr("3")},
		{Name: "level", ParamType: "enum", EnumOptions: []string{"low", "high"}, DefaultValue: strPtr("low")},
	}
	if w := putAutopilotParams(t, apID, set); w.Code != http.StatusOK {
		t.Fatalf("seed params: got %d body=%s", w.Code, w.Body.String())
	}

	// The read surface alone must carry every field needed to construct a call.
	listed := listAutopilotParams(t, apID)
	byName := make(map[string]AutopilotParamResponse, len(listed.Params))
	for _, p := range listed.Params {
		if p.Name == "" || p.ParamType == "" {
			t.Fatalf("contract entry missing name/type: %#v", p)
		}
		byName[p.Name] = p
	}
	city, ok := byName["city"]
	if !ok || !city.Required || city.DefaultValue != nil {
		t.Fatalf("city contract = %#v, want required with no default", city)
	}
	retries := byName["retries"]
	if retries.Required || retries.DefaultValue == nil || *retries.DefaultValue != "3" {
		t.Fatalf("retries contract = %#v, want optional default \"3\"", retries)
	}
	level := byName["level"]
	if len(level.EnumOptions) != 2 || level.DefaultValue == nil || *level.DefaultValue != "low" {
		t.Fatalf("level contract = %#v, want enum low/high defaulting to low", level)
	}

	// Build the trigger body purely from the enumerated contract.
	params := map[string]any{"city": "北京"}
	for _, p := range listed.Params {
		if p.Required || p.DefaultValue == nil {
			continue
		}
		switch p.ParamType {
		case "number":
			n, err := strconv.ParseFloat(*p.DefaultValue, 64)
			if err != nil {
				t.Fatalf("coerce %s default %q: %v", p.Name, *p.DefaultValue, err)
			}
			params[p.Name] = n
		case "boolean":
			params[p.Name] = *p.DefaultValue == "true"
		default:
			params[p.Name] = *p.DefaultValue
		}
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/autopilots/"+apID+"/trigger", map[string]any{"params": params})
	req = withURLParam(req, "id", apID)
	testHandler.TriggerAutopilot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("contract-built trigger: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var runResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	runID, _ := runResp["id"].(string)
	if runID == "" {
		t.Fatalf("run response missing id: %#v", runResp)
	}
	var payload []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT trigger_payload FROM autopilot_run WHERE id = $1`, runID).Scan(&payload); err != nil {
		t.Fatalf("load run payload: %v", err)
	}
	var snap struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("decode snapshot: %v body=%s", err, string(payload))
	}
	if snap.Params["city"] != "北京" {
		t.Fatalf("snapshot city = %#v, want 北京", snap.Params["city"])
	}
	// Defaulted values resolved through their contract types: number → JSON 3.
	if snap.Params["retries"] != float64(3) {
		t.Fatalf("snapshot retries = %#v, want number 3", snap.Params["retries"])
	}
	if snap.Params["level"] != "low" {
		t.Fatalf("snapshot level = %#v, want \"low\"", snap.Params["level"])
	}
}

func TestTriggerAutopilotBodySizeCap(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsCap Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	cleanupAutopilotParams(t, apID)

	// A body past maxTriggerBodyBytes must stop at the read cap (413), not
	// buffer fully and surface later as a parameter-validation 400.
	big := strings.Repeat("x", maxTriggerBodyBytes+1024)
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/autopilots/"+apID+"/trigger", map[string]any{
		"params": map[string]any{"pad": big},
	})
	req = withURLParam(req, "id", apID)
	testHandler.TriggerAutopilot(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: expected 413, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateAutopilotParamsAtomicWithTemplates(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createWebhookTestAgent(t, "ParamsPatch Agent")
	apID := createWebhookTestAutopilot(t, agentID, "active", "run_only")
	cleanupAutopilotParams(t, apID)

	patch := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("PATCH", "/api/autopilots/"+apID+"?workspace_id="+testWorkspaceID, body)
		req = withURLParam(req, "id", apID)
		testHandler.UpdateAutopilot(w, req)
		return w
	}

	// Seed: one param plus a template referencing it.
	if w := putAutopilotParams(t, apID, []service.AutopilotParamInput{cityParam()}); w.Code != http.StatusOK {
		t.Fatalf("seed params: got %d body=%s", w.Code, w.Body.String())
	}

	// Adding a parameter and referencing it in the template is ONE atomic
	// write: the new token validates against the new set within the request.
	w := patch(map[string]any{
		"issue_title_template": "Report for {{city}} / {{region}}",
		"params": []map[string]any{
			{"name": "city", "param_type": "string", "required": true},
			{"name": "region", "param_type": "string"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("atomic add+template: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if listed := listAutopilotParams(t, apID); len(listed.Params) != 2 {
		t.Fatalf("params after atomic patch = %#v, want city+region", listed.Params)
	}

	// Dropping a parameter a stored template still references is rejected and
	// leaves the stored set untouched.
	w = patch(map[string]any{"params": []map[string]any{}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dangling token via patch: expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var errResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if got, _ := errResp["error"].(string); !strings.Contains(got, "city") {
		t.Fatalf("rejection must name the dangling token, got %#v", errResp["error"])
	}
	if listed := listAutopilotParams(t, apID); len(listed.Params) != 2 {
		t.Fatalf("rejected patch must not touch the stored set, got %#v", listed.Params)
	}

	// A patch WITHOUT the params key must not replace the stored set.
	w = patch(map[string]any{"issue_title_template": "Report for {{city}} / {{region}} again"})
	if w.Code != http.StatusOK {
		t.Fatalf("no-params patch: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if listed := listAutopilotParams(t, apID); len(listed.Params) != 2 {
		t.Fatalf("params-key-absent patch must not touch the set, got %#v", listed.Params)
	}
}
