package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// RUYI-425 stage 2: the GET-side instance surface (Type-layer declaration +
// probe-aware credential badge), the §4.3 connectivity probe, and the §4.3
// voice instance settings PATCH. All probe targets are stubbed httptest
// servers — no real provider is ever contacted and only fake key values are
// used throughout.

// insertManualVoiceInstanceFixture is insertVoiceFamilyFixture with
// registration_source='manual' (the shape the §4.3 form targets) and an
// optional metadata bag.
func insertManualVoiceInstanceFixture(t *testing.T, ctx context.Context, name, metadata string) string {
	t.Helper()
	profileID := insertRuntimeProfileFixture(t, ctx, name+" Profile", "gemini_live", "")
	if metadata == "" {
		metadata = "{}"
	}
	var instanceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, profile_id, registration_source
		)
		VALUES ($1, NULL, $2, 'local', 'gemini_live', 'offline', $3, $4::jsonb, $5, $6, 'manual')
		RETURNING id
	`, testWorkspaceID, name, name+" device", metadata, testUserID, profileID).Scan(&instanceID); err != nil {
		t.Fatalf("insert manual voice instance fixture: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, instanceID)
	})
	return instanceID
}

// stubProbeTarget installs an httptest server as the probe base URL and
// restores the previous value at cleanup.
func stubProbeTarget(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	previous := testHandler.VoiceProbeBaseURL
	testHandler.VoiceProbeBaseURL = srv.URL
	t.Cleanup(func() {
		testHandler.VoiceProbeBaseURL = previous
	})
	return srv.URL
}

func listRuntimesForAssertions(t *testing.T) []AgentRuntimeResponse {
	t.Helper()
	listW := testutil.Call(t, testHandler.ListAgentRuntimes,
		newRequest(http.MethodGet, "/api/runtimes", nil),
	).Want(http.StatusOK)
	var runtimes []AgentRuntimeResponse
	if err := json.NewDecoder(listW.Body).Decode(&runtimes); err != nil {
		t.Fatalf("decode runtime list: %v", err)
	}
	return runtimes
}

func findRuntimeByID(t *testing.T, runtimes []AgentRuntimeResponse, id string) AgentRuntimeResponse {
	t.Helper()
	for _, rt := range runtimes {
		if rt.ID == id {
			return rt
		}
	}
	t.Fatalf("runtime %s missing from the list", id)
	return AgentRuntimeResponse{}
}

// TestListAgentRuntimes_InstanceTypeDeclaration pins the GET-side §4.2
// contract: every instance carries its Type-layer family and capability
// declaration so the slot pickers can filter without a second round trip.
func TestListAgentRuntimes_InstanceTypeDeclaration(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	_, voiceInstanceID := insertVoiceFamilyFixture(t, ctx, "Stage2 Voice Decl")
	cliInstanceID := insertProfileRuntimeFixture(t, ctx,
		insertRuntimeProfileFixture(t, ctx, "Stage2 CLI Profile", "kimi", "kimi"),
		"Stage2 CLI Instance", "kimi")

	runtimes := listRuntimesForAssertions(t)

	voice := findRuntimeByID(t, runtimes, voiceInstanceID)
	if voice.ProtocolFamily != "gemini_live" {
		t.Errorf("voice instance protocol_family = %q, want gemini_live", voice.ProtocolFamily)
	}
	wantVoiceCaps := agent.Capabilities{RealtimeVoice: true, Tools: true}
	if voice.Capabilities != wantVoiceCaps {
		t.Errorf("voice instance capabilities = %+v, want %+v", voice.Capabilities, wantVoiceCaps)
	}
	if voice.CredentialStatus != "not_configured" {
		t.Errorf("voice credential_status = %q, want not_configured", voice.CredentialStatus)
	}

	cli := findRuntimeByID(t, runtimes, cliInstanceID)
	if cli.ProtocolFamily != "kimi" {
		t.Errorf("cli instance protocol_family = %q, want kimi", cli.ProtocolFamily)
	}
	wantCLICaps := agent.Capabilities{Text: true, Tools: true}
	if cli.Capabilities != wantCLICaps {
		t.Errorf("cli instance capabilities = %+v, want %+v", cli.Capabilities, wantCLICaps)
	}
}

// TestPutRuntimeCredential_ProbeDrivesBadgeState is the §4.5 tri-state loop
// against a stubbed probe target: a failing probe records "invalid", the
// next save against a working target returns the badge to "configured", and
// the credential value never appears in any response.
func TestPutRuntimeCredential_ProbeDrivesBadgeState(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	credentialTestBox(t)

	instanceID := insertManualVoiceInstanceFixture(t, ctx, "Stage2 Probe Badge", "")
	const fakeKey = "fake-key-never-a-real-credential"

	// Failing probe (401): save still succeeds, badge records invalid.
	stubProbeTarget(t, http.StatusUnauthorized)
	w := putRuntimeCredential(t, instanceID, "api_key", fakeKey)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT with failing probe: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var putResp struct {
		CredentialStatus string `json:"credential_status"`
		Probe            struct {
			Status     string `json:"status"`
			HTTPStatus int    `json:"http_status"`
		} `json:"probe"`
	}
	if err := json.NewDecoder(w.Body).Decode(&putResp); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if strings.Contains(w.Body.String(), fakeKey) {
		t.Fatal("credential value leaked into the PUT response")
	}
	if putResp.Probe.Status != "invalid" || putResp.Probe.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("probe = %+v, want invalid/401", putResp.Probe)
	}

	voice := findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
	if voice.CredentialStatus != "invalid" {
		t.Errorf("badge after failed probe = %q, want invalid", voice.CredentialStatus)
	}

	// Working probe (200): rotation restores the configured badge.
	stubProbeTarget(t, http.StatusOK)
	if w := putRuntimeCredential(t, instanceID, "api_key", fakeKey+"-rotated"); w.Code != http.StatusOK {
		t.Fatalf("rotation PUT: expected 200, got %d", w.Code)
	}
	voice = findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
	if voice.CredentialStatus != "configured" {
		t.Errorf("badge after successful probe = %q, want configured", voice.CredentialStatus)
	}

	// DELETE returns the badge to not_configured and clears the probe state.
	if w := deleteRuntimeCredential(t, instanceID, "api_key"); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204, got %d", w.Code)
	}
	voice = findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
	if voice.CredentialStatus != "not_configured" {
		t.Errorf("badge after delete = %q, want not_configured", voice.CredentialStatus)
	}
}

// TestPutRuntimeCredential_ProbeFailureDoesNotBlockSave pins the §4.5 rule
// that a broken probe target (here: a closed server) never fails the save.
func TestPutRuntimeCredential_ProbeFailureDoesNotBlockSave(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	credentialTestBox(t)

	instanceID := insertManualVoiceInstanceFixture(t, ctx, "Stage2 Probe Dead", "")

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close() // nothing listens anymore: the probe transport will fail
	previous := testHandler.VoiceProbeBaseURL
	testHandler.VoiceProbeBaseURL = dead.URL
	t.Cleanup(func() { testHandler.VoiceProbeBaseURL = previous })

	w := putRuntimeCredential(t, instanceID, "api_key", "fake-key-offline-probe")
	if w.Code != http.StatusOK {
		t.Fatalf("save must not be blocked by a dead probe target: got %d: %s", w.Code, w.Body.String())
	}
	voice := findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
	if voice.CredentialStatus != "invalid" {
		t.Errorf("badge after unreachable probe = %q, want invalid", voice.CredentialStatus)
	}
}

// TestUpdateAgentRuntime_VoiceSettings covers the §4.3 settings PATCH on a
// manual instance: model/advanced/disabled merge into metadata without
// disturbing unrelated keys, clears work, and the list reflects the result.
func TestUpdateAgentRuntime_VoiceSettings(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	instanceID := insertManualVoiceInstanceFixture(t, ctx, "Stage2 Voice Settings", `{"unrelated":"keepme"}`)

	patchVoiceSettings := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPatch, "/api/runtimes/"+instanceID, body)
		req = withURLParam(req, "runtimeId", instanceID)
		testHandler.UpdateAgentRuntime(w, req)
		return w
	}

	if w := patchVoiceSettings(map[string]any{
		"model":    "gemini-3.8-live",
		"advanced": map[string]any{"region": "asia-east1"},
		"disabled": true,
	}); w.Code != http.StatusOK {
		t.Fatalf("voice settings PATCH: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	rt := findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
	metadataBytes, err := json.Marshal(rt.Metadata)
	if err != nil {
		t.Fatalf("re-encode metadata: %v", err)
	}
	var bag map[string]any
	if err := json.Unmarshal(metadataBytes, &bag); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if bag["model"] != "gemini-3.8-live" {
		t.Errorf("metadata.model = %v, want gemini-3.8-live", bag["model"])
	}
	advanced, ok := bag["advanced"].(map[string]any)
	if !ok || advanced["region"] != "asia-east1" {
		t.Errorf("metadata.advanced = %v, want {region: asia-east1}", bag["advanced"])
	}
	if bag["disabled"] != true {
		t.Errorf("metadata.disabled = %v, want true", bag["disabled"])
	}
	if bag["unrelated"] != "keepme" {
		t.Errorf("metadata.unrelated = %v, want keepme (merge must preserve unrelated keys)", bag["unrelated"])
	}

	// Clears: empty model, empty advanced object, disabled=false.
	if w := patchVoiceSettings(map[string]any{
		"model":    "",
		"advanced": map[string]any{},
		"disabled": false,
	}); w.Code != http.StatusOK {
		t.Fatalf("clearing PATCH: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	rt = findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
	metadataBytes, _ = json.Marshal(rt.Metadata)
	bag = map[string]any{}
	if err := json.Unmarshal(metadataBytes, &bag); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	for _, key := range []string{"model", "advanced", "disabled"} {
		if _, present := bag[key]; present {
			t.Errorf("metadata.%s should be cleared, got %v", key, bag[key])
		}
	}
	if bag["unrelated"] != "keepme" {
		t.Errorf("metadata.unrelated = %v, want keepme", bag["unrelated"])
	}

	// Validation: oversize advanced JSON is rejected whole.
	if w := patchVoiceSettings(map[string]any{
		"advanced": map[string]any{"blob": strings.Repeat("x", 5000)},
	}); w.Code != http.StatusBadRequest {
		t.Errorf("oversize advanced: expected 400, got %d", w.Code)
	}
}

// TestUpdateAgentRuntime_VoiceSettingsPartialPatches pins the partial-PATCH
// contract on manual instances: every §4.3 field is optional, so a
// single-field body must succeed (no nil dereference on the absent ones) and
// must leave the other fields' stored keys untouched — absence means "don't
// change", never "clear".
func TestUpdateAgentRuntime_VoiceSettingsPartialPatches(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	instanceID := insertManualVoiceInstanceFixture(t, ctx, "Stage2 Partial Patches",
		`{"model":"gemini-3.8-live","advanced":{"region":"asia-east1"},"disabled":true,"unrelated":"keepme"}`)

	patch := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPatch, "/api/runtimes/"+instanceID, body)
		req = withURLParam(req, "runtimeId", instanceID)
		testHandler.UpdateAgentRuntime(w, req)
		return w
	}

	assertBag := func(wantModel, wantRegion string, wantDisabled any) {
		t.Helper()
		rt := findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
		metadataBytes, err := json.Marshal(rt.Metadata)
		if err != nil {
			t.Fatalf("re-encode metadata: %v", err)
		}
		var bag map[string]any
		if err := json.Unmarshal(metadataBytes, &bag); err != nil {
			t.Fatalf("decode metadata: %v", err)
		}
		if wantModel == "" {
			if _, present := bag["model"]; present {
				t.Errorf("metadata.model should be absent, got %v", bag["model"])
			}
		} else if bag["model"] != wantModel {
			t.Errorf("metadata.model = %v, want %s (untouched by a partial PATCH)", bag["model"], wantModel)
		}
		advanced, present := bag["advanced"].(map[string]any)
		if wantRegion == "" {
			if present {
				t.Errorf("metadata.advanced should be absent, got %v", bag["advanced"])
			}
		} else if !present || advanced["region"] != wantRegion {
			t.Errorf("metadata.advanced = %v, want region %s (untouched by a partial PATCH)", bag["advanced"], wantRegion)
		}
		if wantDisabled == nil {
			if _, present := bag["disabled"]; present {
				t.Errorf("metadata.disabled should be absent, got %v", bag["disabled"])
			}
		} else if bag["disabled"] != wantDisabled {
			t.Errorf("metadata.disabled = %v, want %v", bag["disabled"], wantDisabled)
		}
		if bag["unrelated"] != "keepme" {
			t.Errorf("metadata.unrelated = %v, want keepme", bag["unrelated"])
		}
	}

	// Model-only PATCH must not panic on the absent advanced/disabled fields.
	if w := patch(map[string]any{"model": "gemini-4.0-flash"}); w.Code != http.StatusOK {
		t.Fatalf("model-only PATCH: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertBag("gemini-4.0-flash", "asia-east1", true)

	// Advanced-only PATCH likewise.
	if w := patch(map[string]any{"advanced": map[string]any{"region": "us-central1"}}); w.Code != http.StatusOK {
		t.Fatalf("advanced-only PATCH: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertBag("gemini-4.0-flash", "us-central1", true)

	// Disabled-only PATCH: explicit false removes only the disabled key.
	if w := patch(map[string]any{"disabled": false}); w.Code != http.StatusOK {
		t.Fatalf("disabled-only PATCH: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertBag("gemini-4.0-flash", "us-central1", nil)

	// Disabled-only explicit true re-arms the flag without touching the rest.
	if w := patch(map[string]any{"disabled": true}); w.Code != http.StatusOK {
		t.Fatalf("disabled-only true PATCH: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertBag("gemini-4.0-flash", "us-central1", true)
}

// TestUpdateAgentRuntime_VoiceSettingsRejectsDaemonInstances pins the
// metadata-ownership guard: daemon-registered instances get their metadata
// wholesale from registration, so §4.3 writes there are refused.
func TestUpdateAgentRuntime_VoiceSettingsRejectsDaemonInstances(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	_, instanceID := insertVoiceFamilyFixture(t, ctx, "Stage2 Daemon Voice")

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPatch, "/api/runtimes/"+instanceID, map[string]any{"model": "gemini-3.8-live"})
	req = withURLParam(req, "runtimeId", instanceID)
	testHandler.UpdateAgentRuntime(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH voice settings on daemon instance: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "manually registered") {
		t.Errorf("error = %s, want it to name the manual-registration requirement", w.Body.String())
	}
}

// TestListAgentRuntimes_ProbeBadgeForDaemonShapeInstances guards the badge
// precedence: without a credential_ref the badge stays "not_configured" even
// when metadata carries a stale invalid probe record — the pointer is the
// contract, the probe only colors the configured state.
func TestListAgentRuntimes_ProbeBadgeForDaemonShapeInstances(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	instanceID := insertManualVoiceInstanceFixture(t, ctx, "Stage2 Legacy Badge",
		fmt.Sprintf(`{"%s":{"status":"invalid","http_status":401,"checked_at":"2026-10-05T00:00:00Z"}}`, credentialProbeMetadataKey))

	voice := findRuntimeByID(t, listRuntimesForAssertions(t), instanceID)
	if voice.CredentialStatus != "not_configured" {
		t.Errorf("badge without credential_ref = %q, want not_configured", voice.CredentialStatus)
	}
}
