package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RUYI-626 stage 1: the direct-connect voice session endpoints. The client
// dials the provider itself, so the server's role shrinks to the §4.4 gate,
// the credential hand-off, the live_session row, and the terminal
// transcript/handle write-back. Providers are never contacted by these
// endpoints — only fake key values are used throughout.

// startVoiceDirectSession issues the direct-session creation request with the
// standard header identity (the endpoint sits inside the Auth group).
func startVoiceDirectSession(t *testing.T, agentID string) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPost, "/api/agents/"+agentID+"/voice-direct-session", nil), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.StartVoiceDirectSession(w, req)
	return w
}

func TestVoiceDirectSession_StartReturnsProviderHandoff(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)
	agentID, _ := seedUsableVoiceSetup(t, "Direct Start", `{"model":"gemini-custom","advanced":{"temperature":0.4}}`)

	w := startVoiceDirectSession(t, agentID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		SessionID     string         `json:"session_id"`
		ProviderWSURL string         `json:"provider_ws_url"`
		APIKey        string         `json:"api_key"`
		Model         string         `json:"model"`
		Instructions  string         `json:"instructions"`
		Advanced      map[string]any `json:"advanced"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.SessionID == "" {
		t.Fatalf("payload must carry a live_session id")
	}
	if payload.APIKey != voiceTestAPIKey {
		t.Fatalf("api_key must be the decrypted credential the client dials with")
	}
	if payload.ProviderWSURL == "" || !strings.HasPrefix(payload.ProviderWSURL, "wss://") {
		t.Fatalf("provider_ws_url = %q, want the wss provider endpoint", payload.ProviderWSURL)
	}
	if payload.Model != "gemini-custom" {
		t.Fatalf("model = %q, want the instance metadata's model", payload.Model)
	}
	if payload.Instructions != "VOICE-CONTEXT-MARKER-7q2: follow the injected instructions" {
		t.Fatalf("instructions = %q, want the agent's instructions (the client composes the setup frame now)", payload.Instructions)
	}
	if payload.Advanced["temperature"] != 0.4 {
		t.Fatalf("advanced = %v, want the instance metadata's advanced bag", payload.Advanced)
	}

	// The gate pass creates the same live_session row the gateway used to,
	// tagged mode='direct' so the direct row is distinguishable from — and
	// auditable against — gateway-relayed rows (RUYI-626 P1).
	var status, mode string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status, mode FROM live_session WHERE id = $1`, payload.SessionID,
	).Scan(&status, &mode); err != nil {
		t.Fatalf("load live session: %v", err)
	}
	if status != "active" {
		t.Fatalf("live_session status = %q, want active", status)
	}
	if mode != "direct" {
		t.Fatalf("live_session mode = %q, want direct", mode)
	}
}

// TestOpenVoiceSessionTagsGatewayMode pins the gateway side of the
// live_session mode split (RUYI-626 P1): the shared gate chain creates the
// row for the relay transport, and that row must stay 'gateway' — the value
// migration 937 backfilled every pre-direct row with — so mode='direct'
// remains a precise audit predicate for the direct-connect handoff.
func TestOpenVoiceSessionTagsGatewayMode(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)
	agentID, _ := seedUsableVoiceSetup(t, "Gateway Mode", "")

	plan, hsErr := testHandler.openVoiceSession(context.Background(), testUserID, testWorkspaceID, parseUUID(agentID), voiceSessionModeGateway)
	if hsErr != nil {
		t.Fatalf("openVoiceSession rejected a usable setup: %+v", hsErr)
	}

	var mode string
	if err := testPool.QueryRow(context.Background(),
		`SELECT mode FROM live_session WHERE id = $1`, uuidToString(plan.session.ID),
	).Scan(&mode); err != nil {
		t.Fatalf("load live session: %v", err)
	}
	if mode != "gateway" {
		t.Fatalf("live_session mode = %q, want gateway", mode)
	}
}

func TestVoiceDirectSession_GateReusesTheRule3Chain(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)

	t.Run("instance_disabled", func(t *testing.T) {
		instanceID := insertOnlineVoiceInstanceFixture(t, "Direct Disabled Instance", `{"disabled":true}`)
		agentID := insertVoiceBoundAgentFixture(t, "Direct Disabled Agent", instanceID)
		expectVoiceUnavailable(t, "direct", startVoiceDirectSession(t, agentID), "instance_disabled")
	})

	t.Run("credential_missing", func(t *testing.T) {
		instanceID := insertOnlineVoiceInstanceFixture(t, "Direct NoKey Instance", "")
		agentID := insertVoiceBoundAgentFixture(t, "Direct NoKey Agent", instanceID)
		expectVoiceUnavailable(t, "direct", startVoiceDirectSession(t, agentID), "credential_missing")
	})
}

// completeVoiceDirectSession posts the client-collected record back.
func completeVoiceDirectSession(t *testing.T, sessionID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPost, "/api/voice-sessions/"+sessionID+"/complete", body),
		"sessionId", sessionID)
	w := httptest.NewRecorder()
	testHandler.CompleteVoiceDirectSession(w, req)
	return w
}

func TestVoiceDirectSession_CompletePersistsAndWritesBack(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)
	agentID, _ := seedUsableVoiceSetup(t, "Direct Complete", "")

	startw := startVoiceDirectSession(t, agentID)
	if startw.Code != http.StatusOK {
		t.Fatalf("start: %d %s", startw.Code, startw.Body.String())
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(startw.Body.Bytes(), &started)

	w := completeVoiceDirectSession(t, started.SessionID, map[string]any{
		"transcript": []map[string]any{
			{"role": "user", "text": "帮我看看构建日志", "at": "2026-10-10T03:00:00Z"},
			{"role": "assistant", "text": "好的，日志显示编译失败。", "at": "2026-10-10T03:00:05Z"},
		},
		"session_handle": "resumption-handle-abc",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}

	ctx := context.Background()
	var status, handle, summary string
	var transcript []map[string]any
	var endedAt *time.Time
	if err := testPool.QueryRow(ctx,
		`SELECT status, transcript, session_handle, summary, ended_at FROM live_session WHERE id = $1`,
		started.SessionID,
	).Scan(&status, &transcript, &handle, &summary, &endedAt); err != nil {
		t.Fatalf("load live session: %v", err)
	}
	if status != "ended" || endedAt == nil {
		t.Fatalf("session must be terminal after complete: status=%v ended_at=%v", status, endedAt)
	}
	if len(transcript) != 2 || transcript[0]["text"] != "帮我看看构建日志" {
		t.Fatalf("transcript = %v", transcript)
	}
	if handle != "resumption-handle-abc" {
		t.Fatalf("session_handle = %v", handle)
	}
	if summary == "" {
		t.Fatalf("the write-back summary projection must land with the transcript")
	}
}

func TestVoiceDirectSession_CompleteIsIdempotentAndScoped(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)
	agentID, _ := seedUsableVoiceSetup(t, "Direct Idempotent", "")

	startw := startVoiceDirectSession(t, agentID)
	var started struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(startw.Body.Bytes(), &started)

	first := completeVoiceDirectSession(t, started.SessionID, map[string]any{
		"transcript": []map[string]any{{"role": "user", "text": "one", "at": "2026-10-10T03:00:00Z"}},
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first complete: %d %s", first.Code, first.Body.String())
	}
	var endedAt1 time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT ended_at FROM live_session WHERE id = $1`, started.SessionID).Scan(&endedAt1); err != nil {
		t.Fatalf("load ended_at after first complete: %v", err)
	}

	// A client retry must not rewrite the terminal row or re-run write-back.
	second := completeVoiceDirectSession(t, started.SessionID, map[string]any{
		"transcript": []map[string]any{{"role": "user", "text": "two", "at": "2026-10-10T03:00:09Z"}},
	})
	if second.Code != http.StatusOK {
		t.Fatalf("second complete: %d %s", second.Code, second.Body.String())
	}
	var endedAt2 time.Time
	var transcript []map[string]any
	if err := testPool.QueryRow(context.Background(),
		`SELECT ended_at, transcript FROM live_session WHERE id = $1`, started.SessionID).Scan(&endedAt2, &transcript); err != nil {
		t.Fatalf("load terminal row after replay: %v", err)
	}
	if !endedAt1.Equal(endedAt2) {
		t.Fatalf("a replayed complete rewrote the terminal row (%v → %v)", endedAt1, endedAt2)
	}
	var texts []string
	for _, e := range transcript {
		texts = append(texts, e["text"].(string))
	}
	if len(texts) != 1 || texts[0] != "one" {
		t.Fatalf("a replayed complete overwrote the first transcript: %v", texts)
	}

	// Another user's session is not theirs to complete.
	stranger := withURLParam(newRequestAs("stranger-user-not-a-member", http.MethodPost,
		"/api/voice-sessions/"+started.SessionID+"/complete", map[string]any{}), "sessionId", started.SessionID)
	sw := httptest.NewRecorder()
	testHandler.CompleteVoiceDirectSession(sw, stranger)
	if sw.Code != http.StatusNotFound {
		t.Fatalf("stranger complete: %d %s", sw.Code, sw.Body.String())
	}
}

func TestVoiceDirectSession_CompleteBoundsTheBody(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)
	agentID, _ := seedUsableVoiceSetup(t, "Direct Bound", "")

	startw := startVoiceDirectSession(t, agentID)
	var started struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(startw.Body.Bytes(), &started)

	huge := map[string]any{
		"transcript": []map[string]any{{"role": "user", "text": strings.Repeat("x", 2*1024*1024)}},
	}
	w := completeVoiceDirectSession(t, started.SessionID, huge)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for an oversized transcript, got %d", w.Code)
	}
}

// reportCredentialProbe posts a client-side probe outcome.
func reportCredentialProbe(t *testing.T, runtimeID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPost, "/api/runtimes/"+runtimeID+"/credential-probe", body),
		"runtimeId", runtimeID)
	w := httptest.NewRecorder()
	testHandler.ReportCredentialProbe(w, req)
	return w
}

func TestReportCredentialProbe_RecordsClientOutcome(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)
	agentID, instanceID := seedUsableVoiceSetup(t, "Probe Report", "")

	for _, tc := range []struct {
		status string
		want   string
	}{
		{"ok", "ok"},
		{"unreachable", "unreachable"},
		{"invalid", "invalid"},
	} {
		w := reportCredentialProbe(t, instanceID, map[string]any{"status": tc.status, "http_status": 429})
		if w.Code != http.StatusOK {
			t.Fatalf("report %s: %d %s", tc.status, w.Code, w.Body.String())
		}
		var metadata []byte
		if err := testPool.QueryRow(context.Background(),
			`SELECT metadata FROM agent_runtime WHERE id = $1`, instanceID,
		).Scan(&metadata); err != nil {
			t.Fatalf("load metadata: %v", err)
		}
		var bag map[string]json.RawMessage
		json.Unmarshal(metadata, &bag)
		var probe struct {
			Status string `json:"status"`
		}
		json.Unmarshal(bag["credential_probe"], &probe)
		if probe.Status != tc.want {
			t.Fatalf("probe status = %q, want %q", probe.Status, tc.want)
		}
	}

	// An unknown status is rejected, never stored.
	bad := reportCredentialProbe(t, instanceID, map[string]any{"status": "spectacular"})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown status: %d %s", bad.Code, bad.Body.String())
	}

	// "unreachable" is a network verdict, not a credential verdict: a direct
	// session start must not be blocked by it (RUYI-619 boundary — only
	// "invalid" gates).
	stubProbeTarget(t, http.StatusServiceUnavailable)
	reportCredentialProbe(t, instanceID, map[string]any{"status": "unreachable"})
	if w := startVoiceDirectSession(t, agentID); w.Code != http.StatusOK {
		t.Fatalf("unreachable probe must not block a direct start: %d %s", w.Code, w.Body.String())
	}

	// "invalid" keeps gating.
	stubProbeTarget(t, http.StatusUnauthorized)
	instanceID2 := insertOnlineVoiceInstanceFixture(t, "Probe Invalid Instance", "")
	putRuntimeCredential(t, instanceID2, "api_key", voiceTestAPIKey)
	agentID2 := insertVoiceBoundAgentFixture(t, "Probe Invalid Agent", instanceID2)
	expectVoiceUnavailable(t, "invalid gate", startVoiceDirectSession(t, agentID2), "credential_invalid")

	// A client report of "ok" self-heals the stale server-side verdict (the
	// RUYI-603 存量误报 path): the same start now passes.
	stubProbeTarget(t, http.StatusOK)
	if w := reportCredentialProbe(t, instanceID2, map[string]any{"status": "ok", "http_status": 200}); w.Code != http.StatusOK {
		t.Fatalf("self-heal report: %d %s", w.Code, w.Body.String())
	}
	if w := startVoiceDirectSession(t, agentID2); w.Code == http.StatusConflict {
		t.Fatalf("a client-reported ok must lift the credential_invalid gate: %s", w.Body.String())
	}
}
