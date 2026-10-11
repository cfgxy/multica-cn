package main

// Route-layer regression tests for the RUYI-630 decision-request surface.
// The 2026-10-11 QA round-trip found CreateDecisionRequest thoroughly tested
// through direct handler calls while the POST route itself had never been
// registered — the integration gap this file closes: every request here goes
// through NewRouter (auth middleware → workspace membership → chi route
// matching → handler), so a missing registration fails the test instead of
// shipping. The cross-space assertion additionally pins the dual-row
// landing and the server-resolved origin agent name (QA defect#3).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/handler"
)

// decisionRequestRouteFixture carries the IDs a test needs to assert and to
// clean up after itself.
type decisionRequestRouteFixture struct {
	runtimeID  string
	agentID    string
	taskID     string
	targetWSID string
	matToken   string
	groupID    string // filled from the create response
}

// seedDecisionRequestRouteFixture mints a real mat_ task token for a fresh
// agent in the fixture workspace, plus a bare target workspace for the
// cross-space variant. Cleanup runs even when the create fails midway.
func seedDecisionRequestRouteFixture(t *testing.T) decisionRequestRouteFixture {
	t.Helper()
	ctx := context.Background()
	fx := decisionRequestRouteFixture{
		runtimeID:  uuid.NewString(),
		agentID:    uuid.NewString(),
		taskID:     uuid.NewString(),
		targetWSID: uuid.NewString(),
		matToken:   "mat_" + uuid.NewString() + uuid.NewString(),
	}
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := testPool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	mustExec(`INSERT INTO agent_runtime (id, workspace_id, daemon_id, name, runtime_mode, provider,
		status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1::uuid, $2::uuid, NULL, 'route-test-runtime', 'cloud', 'decision_request_routes_test',
		'online', '', '{}'::jsonb, $3::uuid, now())`, fx.runtimeID, testWorkspaceID, testUserID)
	mustExec(`INSERT INTO agent (id, workspace_id, name, description, runtime_mode, runtime_config,
		visibility, permission_mode, max_concurrent_tasks, owner_id, runtime_id)
		VALUES ($1::uuid, $2::uuid, 'route-test-agent', '', 'cloud', '{}'::jsonb,
		'private', 'private', 1, $3::uuid, $4::uuid)`, fx.agentID, testWorkspaceID, testUserID, fx.runtimeID)
	mustExec(`INSERT INTO agent_task_queue (id, agent_id, status, priority, runtime_id)
		VALUES ($1::uuid, $2::uuid, 'queued', 0, $3::uuid)`, fx.taskID, fx.agentID, fx.runtimeID)
	mustExec(`INSERT INTO task_token (id, token_hash, task_id, agent_id, workspace_id, user_id, expires_at)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, now() + interval '1 hour')`,
		uuid.NewString(), auth.HashToken(fx.matToken), fx.taskID, fx.agentID, testWorkspaceID, testUserID)
	mustExec(`INSERT INTO workspace (id, name, slug, description, issue_prefix)
		VALUES ($1::uuid, 'route-test-target', $2, '', '')`,
		fx.targetWSID, "route-test-target-"+uuid.NewString()[:8])

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM decision_requests WHERE workspace_id = ANY($1::uuid[])`,
			[]string{testWorkspaceID, fx.targetWSID})
		if fx.groupID != "" {
			testPool.Exec(ctx, `DELETE FROM audit_event WHERE trigger_ref = $1`, fx.groupID)
		}
		testPool.Exec(ctx, `DELETE FROM task_token WHERE token_hash = $1`, auth.HashToken(fx.matToken))
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1::uuid`, fx.taskID)
		testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1::uuid`, fx.agentID)
		testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1::uuid`, fx.runtimeID)
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1::uuid`, fx.targetWSID)
	})
	return fx
}

// decisionRequestRouteDo fires one real HTTP request at the integration
// test server with the given bearer credential.
func decisionRequestRouteDo(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var payload io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		payload = &buf
	}
	req, err := http.NewRequest(method, testServer.URL+path, payload)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestDecisionRequestCreateRouteRegistered(t *testing.T) {
	if testServer == nil || testPool == nil {
		t.Skip("no integration fixture")
	}
	fx := seedDecisionRequestRouteFixture(t)

	// Agent credential through the real router: 201, the cross-space
	// carrier lands both rows, and the origin agent's name rides on the
	// wire (a target-space roster never contains it).
	resp := decisionRequestRouteDo(t, http.MethodPost,
		"/api/workspaces/"+testWorkspaceID+"/decision-requests", fx.matToken, map[string]any{
			"action_type":         "workspace_info_read",
			"title":               "route smoke",
			"target_workspace_id": fx.targetWSID,
		})
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("agent create = %d: %s", resp.StatusCode, body)
	}
	var created handler.DecisionRequestDetailResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create response: %v (%s)", err, body)
	}
	fx.groupID = created.Request.RequestGroupID
	if created.Request.Role != "origin" {
		t.Fatalf("origin row role = %q, want origin", created.Request.Role)
	}
	if created.Request.OriginAgentName == nil || *created.Request.OriginAgentName != "route-test-agent" {
		t.Fatalf("origin agent name = %v, want route-test-agent (server-resolved)", created.Request.OriginAgentName)
	}
	if len(created.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (origin + target rows)", len(created.Steps))
	}
	roles := map[string]bool{}
	for _, step := range created.Steps {
		roles[step.Role] = true
	}
	if !roles["origin"] || !roles["target"] {
		t.Fatalf("step roles = %v, want origin+target", roles)
	}

	// Both rows really landed, one per involved space.
	var rowCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM decision_requests WHERE request_group_id = $1::uuid`,
		fx.groupID).Scan(&rowCount); err != nil {
		t.Fatalf("count group rows: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("decision_requests rows for group = %d, want 2", rowCount)
	}

	// The same POST with member credentials must be rejected by the
	// handler's agent-only gate — a 403, not the 404/405 a missing route
	// would answer with. This is the exact shape the QA defect#1 escaped
	// through direct-handler-call tests.
	memberResp := decisionRequestRouteDo(t, http.MethodPost,
		"/api/workspaces/"+testWorkspaceID+"/decision-requests", testToken, map[string]any{
			"action_type": "workspace_info_read",
			"title":       "member credentials must not raise requests",
		})
	memberBody, _ := io.ReadAll(memberResp.Body)
	if memberResp.StatusCode != http.StatusForbidden {
		t.Fatalf("member create = %d (%s), want 403 from the registered route", memberResp.StatusCode, memberBody)
	}

	// List and detail carry the resolved name for this space's readers.
	listResp := decisionRequestRouteDo(t, http.MethodGet,
		"/api/workspaces/"+testWorkspaceID+"/decision-requests", testToken, nil)
	listBody, _ := io.ReadAll(listResp.Body)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d: %s", listResp.StatusCode, listBody)
	}
	var list struct {
		Items []handler.DecisionRequestResponse `json:"items"`
	}
	if err := json.Unmarshal(listBody, &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, listBody)
	}
	foundListed := false
	for _, item := range list.Items {
		if item.RequestGroupID == fx.groupID {
			foundListed = true
			if item.OriginAgentName == nil || *item.OriginAgentName != "route-test-agent" {
				t.Fatalf("list origin agent name = %v, want route-test-agent", item.OriginAgentName)
			}
		}
	}
	if !foundListed {
		t.Fatalf("created group %s missing from the list response", fx.groupID)
	}

	detailResp := decisionRequestRouteDo(t, http.MethodGet,
		"/api/workspaces/"+testWorkspaceID+"/decision-requests/"+created.Request.ID, testToken, nil)
	detailBody, _ := io.ReadAll(detailResp.Body)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail = %d: %s", detailResp.StatusCode, detailBody)
	}
	var detail handler.DecisionRequestDetailResponse
	if err := json.Unmarshal(detailBody, &detail); err != nil {
		t.Fatalf("decode detail: %v (%s)", err, detailBody)
	}
	if detail.Request.OriginAgentName == nil || *detail.Request.OriginAgentName != "route-test-agent" {
		t.Fatalf("detail origin agent name = %v, want route-test-agent", detail.Request.OriginAgentName)
	}
}
