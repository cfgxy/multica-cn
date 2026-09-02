package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// All tests in this file require a working database WITH the RUYI-52
// migration (441_agent_webhooks) applied — `make test` migrates first.
// testHandler / testPool / testWorkspaceID / testUserID / testRuntimeID are
// wired in TestMain (handler_test.go); the suite skips when Postgres is
// unreachable.

// ── Fixtures ────────────────────────────────────────────────────────────────

func createAgentWebhookRow(t *testing.T, agentID, prompt string, enabled bool) db.AgentWebhook {
	t.Helper()
	var row db.AgentWebhook
	token, err := generateWebhookToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_webhook (workspace_id, agent_id, name, prompt, token, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, workspace_id, agent_id, name, prompt, token, enabled, created_by, created_at, updated_at
	`, testWorkspaceID, agentID, "hook", prompt, token, enabled, testUserID).Scan(
		&row.ID, &row.WorkspaceID, &row.AgentID, &row.Name, &row.Prompt,
		&row.Token, &row.Enabled, &row.CreatedBy, &row.CreatedAt, &row.UpdatedAt,
	); err != nil {
		t.Fatalf("create agent_webhook: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_webhook WHERE id = $1`, row.ID)
	})
	return row
}

func hitAgentWebhook(t *testing.T, method, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/webhooks/agents/"+token, nil)
	} else {
		req = httptest.NewRequest(method, "/api/webhooks/agents/"+token, strings.NewReader(body))
	}
	req = withURLParam(req, "token", token)
	w := httptest.NewRecorder()
	testHandler.HandleAgentWebhook(w, req)
	return w
}

func countAgentWebhookSessions(t *testing.T, webhook db.AgentWebhook) (sessions, messages int) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(DISTINCT cs.id), count(m.id)
		FROM chat_session cs
		JOIN chat_message m ON m.chat_session_id = cs.id AND m.role = 'user'
		JOIN agent_webhook aw ON cs.workspace_id = aw.workspace_id AND cs.agent_id = aw.agent_id
		WHERE aw.id = $1
	`, webhook.ID).Scan(&sessions, &messages); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return sessions, messages
}

// ── Ingress: the five external protocol input classes ───────────────────────

func setupIngressAgent(t *testing.T) string {
	t.Helper()
	requireDB(t)
	return createWebhookTestAgent(t, "RUYI-52 ingress agent")
}

// Class 1: GET (browser direct open) creates a session seeded with the bound
// prompt, owned by the webhook's creator.
func TestAgentWebhookIngress_GETCreatesSessionWithPrompt(t *testing.T) {
	agentID := setupIngressAgent(t)
	prompt := "检查仓库最新提交，输出变更摘要"
	row := createAgentWebhookRow(t, agentID, prompt, true)

	beforeSessions, _ := countAgentWebhookSessions(t, row)

	w := hitAgentWebhook(t, "GET", row.Token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
		TaskID    string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.Status != "accepted" || resp.SessionID == "" || resp.TaskID == "" {
		t.Fatalf("expected accepted with ids, got %#v", resp)
	}

	afterSessions, messages := countAgentWebhookSessions(t, row)
	if afterSessions != beforeSessions+1 || messages != beforeSessions+1 {
		t.Fatalf("GET must add exactly one session+message, got sessions %d->%d messages=%d", beforeSessions, afterSessions, messages)
	}

	var content, creatorID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT m.content, cs.creator_id
		FROM chat_session cs
		JOIN chat_message m ON m.chat_session_id = cs.id AND m.role = 'user'
		WHERE cs.id = $1
	`, resp.SessionID).Scan(&content, &creatorID); err != nil {
		t.Fatalf("load created session: %v", err)
	}
	if content != prompt {
		t.Fatalf("first message = %q, want the bound prompt %q", content, prompt)
	}
	if creatorID != testUserID {
		t.Fatalf("session creator = %q, want webhook creator %q", creatorID, testUserID)
	}
}

// Class 2: POST with an empty body behaves exactly like GET.
func TestAgentWebhookIngress_POSTEmptyBodyMatchesGET(t *testing.T) {
	agentID := setupIngressAgent(t)
	row := createAgentWebhookRow(t, agentID, "summarize the run", true)
	beforeSessions, _ := countAgentWebhookSessions(t, row)

	w := hitAgentWebhook(t, "POST", row.Token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("POST empty: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "accepted" {
		t.Fatalf("expected accepted, got %#v", resp)
	}
	afterSessions, _ := countAgentWebhookSessions(t, row)
	if afterSessions != beforeSessions+1 {
		t.Fatalf("POST empty must create a session: %d -> %d", beforeSessions, afterSessions)
	}
}

// Class 3: POST with an arbitrary body still only uses the bound prompt —
// the request body is never read.
func TestAgentWebhookIngress_POSTArbitraryBodyIgnored(t *testing.T) {
	agentID := setupIngressAgent(t)
	prompt := "prompt only, body must not leak"
	row := createAgentWebhookRow(t, agentID, prompt, true)

	w := hitAgentWebhook(t, "POST", row.Token, `{"evil":"payload that must never reach the prompt","n":42}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST arbitrary: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var content string
	if err := testPool.QueryRow(context.Background(), `
		SELECT m.content FROM chat_message m
		WHERE m.chat_session_id = $1 AND m.role = 'user'
	`, resp.SessionID).Scan(&content); err != nil {
		t.Fatalf("load message: %v", err)
	}
	if content != prompt {
		t.Fatalf("body leaked into the prompt: message=%q prompt=%q", content, prompt)
	}
	if strings.Contains(content, "payload") {
		t.Fatal("request body content reached the session")
	}
}

// Class 4: a disabled webhook is a terminal no-op — 200 ignored, no session.
func TestAgentWebhookIngress_DisabledWebhookIgnored(t *testing.T) {
	agentID := setupIngressAgent(t)
	row := createAgentWebhookRow(t, agentID, "should not run", false)
	beforeSessions, _ := countAgentWebhookSessions(t, row)

	for _, method := range []string{"GET", "POST"} {
		w := hitAgentWebhook(t, method, row.Token, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s disabled: expected 200, got %d", method, w.Code)
		}
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp["status"] != "ignored" || resp["reason_code"] != "webhook_disabled" {
			t.Fatalf("%s disabled: expected ignored/webhook_disabled, got %#v", method, resp)
		}
		if _, hasSession := resp["session_id"]; hasSession {
			t.Fatalf("ignored response must not carry ids: %#v", resp)
		}
	}
	afterSessions, _ := countAgentWebhookSessions(t, row)
	if afterSessions != beforeSessions {
		t.Fatalf("disabled webhook must not create sessions: %d -> %d", beforeSessions, afterSessions)
	}
}

// Class 5: an unknown token gets the same generic 404 as any other
// unresolvable state — no hint about what tokens exist.
func TestAgentWebhookIngress_UnknownTokenGeneric404(t *testing.T) {
	setupIngressAgent(t)
	w := hitAgentWebhook(t, "GET", "awt_does_not_exist", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "webhook not found" {
		t.Fatalf("generic message drifted: %#v", resp)
	}
}

// Rotation invalidates the old credential immediately; the new one works.
func TestAgentWebhookIngress_RotatedTokenInvalidatesOld(t *testing.T) {
	agentID := setupIngressAgent(t)
	row := createAgentWebhookRow(t, agentID, "rotate me", true)

	oldToken := row.Token
	rotated, err := generateWebhookToken()
	if err != nil {
		t.Fatalf("generate rotated token: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_webhook SET token = $1 WHERE id = $2`, rotated, row.ID); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	if w := hitAgentWebhook(t, "GET", oldToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("old token must 404 after rotation, got %d", w.Code)
	}
	if w := hitAgentWebhook(t, "GET", rotated, ""); w.Code != http.StatusOK {
		t.Fatalf("new token must work after rotation, got %d body=%s", w.Code, w.Body.String())
	}
}

// ── CRUD: permissions, immutability, limit ──────────────────────────────────

func TestAgentWebhookCRUD_ManagerLifecycleAndTokenPolicy(t *testing.T) {
	agentID := setupIngressAgent(t)

	// Create.
	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/agents/"+agentID+"/webhooks", map[string]any{
		"name": "push", "prompt": "check the push",
	}), "id", agentID)
	testHandler.CreateAgentWebhook(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created AgentWebhookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.WebhookToken == nil || !strings.HasPrefix(*created.WebhookToken, "awt_") {
		t.Fatalf("create must return an awt_ token, got %#v", created.WebhookToken)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_webhook WHERE id = $1`, created.ID)
	})

	// Update must keep the token; only name/prompt move.
	w = httptest.NewRecorder()
	req = withURLParams(newRequest("PUT", "/api/agents/"+agentID+"/webhooks/"+created.ID, map[string]any{
		"name": "renamed", "prompt": "new prompt",
	}), "id", agentID, "webhookId", created.ID)
	testHandler.UpdateAgentWebhook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var updated AgentWebhookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.Name != "renamed" || updated.Prompt != "new prompt" {
		t.Fatalf("update lost fields: %#v", updated)
	}
	if updated.WebhookToken == nil || *updated.WebhookToken != *created.WebhookToken {
		t.Fatalf("update must not rotate the token: %#v vs %#v", updated.WebhookToken, created.WebhookToken)
	}

	// Disable stops the ingress.
	w = httptest.NewRecorder()
	req = withURLParams(newRequest("PUT", "/api/agents/"+agentID+"/webhooks/"+created.ID+"/enabled", map[string]any{
		"enabled": false,
	}), "id", agentID, "webhookId", created.ID)
	testHandler.SetAgentWebhookEnabled(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if resp := hitAgentWebhook(t, "GET", *created.WebhookToken, ""); resp.Code != http.StatusOK ||
		!strings.Contains(resp.Body.String(), "webhook_disabled") {
		t.Fatalf("disabled webhook must be ignored: %d %s", resp.Code, resp.Body.String())
	}

	// Delete removes the credential: the URL is dead afterwards.
	w = httptest.NewRecorder()
	req = withURLParams(newRequest("DELETE", "/api/agents/"+agentID+"/webhooks/"+created.ID, nil), "id", agentID, "webhookId", created.ID)
	testHandler.DeleteAgentWebhook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if resp := hitAgentWebhook(t, "GET", *created.WebhookToken, ""); resp.Code != http.StatusNotFound {
		t.Fatalf("deleted webhook URL must 404, got %d", resp.Code)
	}
}

func TestAgentWebhookCRUD_NonManagerSeesNoCredential(t *testing.T) {
	// The fixture agent is PRIVATE and owned by ownerID (a plain member):
	//   - a third member can neither view the agent nor list its webhooks;
	//   - the agent's owner (plain member role) CAN manage and sees tokens.
	agentID, ownerID, memberID := privateAgentTestFixture(t)
	row := createAgentWebhookRow(t, agentID, "member view", true)

	// An unrelated plain member: list is forbidden by the private-agent view
	// gate — webhooks of an invisible agent must not be enumerable.
	w := httptest.NewRecorder()
	req := withURLParam(newRequestAs(memberID, "GET", "/api/agents/"+agentID+"/webhooks", nil), "id", agentID)
	testHandler.ListAgentWebhooks(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member list on private agent: expected 403, got %d body=%s", w.Code, w.Body.String())
	}

	// The agent owner is a manager despite the plain member role: list works
	// and credential fields are present.
	w = httptest.NewRecorder()
	req = withURLParam(newRequestAs(ownerID, "GET", "/api/agents/"+agentID+"/webhooks", nil), "id", agentID)
	testHandler.ListAgentWebhooks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner list: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var rows []AgentWebhookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(rows))
	}
	if rows[0].WebhookToken == nil || *rows[0].WebhookToken != row.Token {
		t.Fatalf("owner must receive the token, got %#v", rows[0].WebhookToken)
	}
	if strings.Contains(rows[0].WebhookPathMasked, row.Token) {
		t.Fatalf("masked path leaked the token: %q", rows[0].WebhookPathMasked)
	}

	// The non-manager cannot create, update, rotate, or delete.
	for _, tc := range []struct {
		method, path string
		body         any
		handler      func(w *httptest.ResponseRecorder, r *http.Request)
	}{
		{"POST", "/api/agents/" + agentID + "/webhooks", map[string]any{"name": "x", "prompt": "y"}, func(w *httptest.ResponseRecorder, r *http.Request) { testHandler.CreateAgentWebhook(w, r) }},
		{"PUT", "/api/agents/" + agentID + "/webhooks/" + uuidToString(row.ID), map[string]any{"name": "x", "prompt": "y"}, func(w *httptest.ResponseRecorder, r *http.Request) { testHandler.UpdateAgentWebhook(w, r) }},
		{"PUT", "/api/agents/" + agentID + "/webhooks/" + uuidToString(row.ID) + "/enabled", map[string]any{"enabled": false}, func(w *httptest.ResponseRecorder, r *http.Request) { testHandler.SetAgentWebhookEnabled(w, r) }},
		{"POST", "/api/agents/" + agentID + "/webhooks/" + uuidToString(row.ID) + "/rotate", nil, func(w *httptest.ResponseRecorder, r *http.Request) { testHandler.RotateAgentWebhook(w, r) }},
		{"DELETE", "/api/agents/" + agentID + "/webhooks/" + uuidToString(row.ID), nil, func(w *httptest.ResponseRecorder, r *http.Request) { testHandler.DeleteAgentWebhook(w, r) }},
	} {
		w := httptest.NewRecorder()
		req := withURLParams(newRequestAs(memberID, tc.method, tc.path, tc.body), "id", agentID, "webhookId", uuidToString(row.ID))
		tc.handler(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s as member: expected 403, got %d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestAgentWebhookCRUD_LimitTwentyPerAgent(t *testing.T) {
	agentID := setupIngressAgent(t)

	rows := make([]db.AgentWebhook, 0, maxAgentWebhooksPerAgent)
	t.Cleanup(func() {
		for _, r := range rows {
			testPool.Exec(context.Background(), `DELETE FROM agent_webhook WHERE id = $1`, r.ID)
		}
	})
	for i := 0; i < maxAgentWebhooksPerAgent; i++ {
		rows = append(rows, createAgentWebhookRow(t, agentID, "limit filler", true))
	}

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/agents/"+agentID+"/webhooks", map[string]any{
		"name": "one too many", "prompt": "nope",
	}), "id", agentID)
	testHandler.CreateAgentWebhook(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 at the limit, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "webhook limit reached") == false {
		t.Fatalf("expected the limit message, got %s", w.Body.String())
	}
}
