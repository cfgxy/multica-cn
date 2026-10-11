package handler

// RUYI-693 stage D2 tests. Two layers:
//
//  1. Pure-logic pins for the chain's shape helpers — the tolerant settings
//     read, the model normalizer, the ref shape contract, the gateway type
//     map, the probe-invalid badge rule and the advanced normalizer.
//
//  2. DB-backed coverage of the six acceptance branches of the resolution
//     chain (agent override hit / workspace default hit / both absent /
//     invalid override → vanilla / unreachable probe → not blocking /
//     credential save resets the probe badge), the credential lifecycle on
//     pool rows, the delete path's unbind-everything transaction, and a
//     replay of migration 961's DML over freshly seeded legacy instances.
//
// Chain results are level-wise atomic by owner decision: a broken level
// falls back to vanilla and never cascades into the next level — several
// tests below pin exactly that.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Pure-logic pins
// ---------------------------------------------------------------------------

func TestParseWorkspaceVoiceSettings(t *testing.T) {
	t.Parallel()

	if got := parseWorkspaceVoiceSettings(nil); got != nil {
		t.Errorf("nil settings must read as absent, got %+v", got)
	}
	if got := parseWorkspaceVoiceSettings([]byte(`not json`)); got != nil {
		t.Errorf("garbage settings must read as absent, got %+v", got)
	}
	if got := parseWorkspaceVoiceSettings([]byte(`{"other":1}`)); got != nil {
		t.Errorf("settings without the voice key must read as absent, got %+v", got)
	}
	if got := parseWorkspaceVoiceSettings([]byte(`{"voice":"broken"}`)); got != nil {
		t.Errorf("malformed voice doc must read as absent (tolerant read), got %+v", got)
	}

	voice := parseWorkspaceVoiceSettings([]byte(`{"voice":{"default":{"provider_id":"d0"},"asr":{"provider_id":"a1","model":"m"}}}`))
	if voice == nil || voice.Default == nil || voice.ASR == nil {
		t.Fatalf("expected both docs parsed, got %+v", voice)
	}
	if voice.Default.ProviderID != "d0" || voice.ASR.ProviderID != "a1" || voice.ASR.Model != "m" {
		t.Errorf("parsed refs mismatch: %+v", voice)
	}
}

func TestNormalizeVoiceModel(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"gemini-2.5-flash":         "gemini-2.5-flash",
		"  models/gemini-2.5-pro ": "gemini-2.5-pro",
		"models/models/x":          "models/x",
		"":                         "",
		"   ":                      "",
	} {
		if got := normalizeVoiceModel(raw); got != want {
			t.Errorf("normalizeVoiceModel(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestValidateVoiceProviderRef(t *testing.T) {
	t.Parallel()

	if err := validateVoiceProviderRef(nil); err != nil {
		t.Errorf("nil ref is absent, not broken: %v", err)
	}
	if err := validateVoiceProviderRef(&voiceProviderRef{}); err != errVoiceRefProviderRequired {
		t.Errorf("empty provider_id: got %v, want errVoiceRefProviderRequired", err)
	}
	if err := validateVoiceProviderRef(&voiceProviderRef{ProviderID: "not-a-uuid"}); err != errVoiceRefProviderInvalid {
		t.Errorf("malformed provider_id: got %v, want errVoiceRefProviderInvalid", err)
	}
	if err := validateVoiceProviderRef(&voiceProviderRef{ProviderID: testWorkspaceID, Model: strings.Repeat("m", 201)}); err != errVoiceRefModelTooLong {
		t.Errorf("overlong model: got %v, want errVoiceRefModelTooLong", err)
	}
	if err := validateVoiceProviderRef(&voiceProviderRef{ProviderID: testWorkspaceID, Model: "models/x"}); err != nil {
		t.Errorf("well-formed ref must pass: %v", err)
	}
}

func TestGatewayServableVoiceProviderType(t *testing.T) {
	t.Parallel()

	family, ok := gatewayServableVoiceProviderType("gemini")
	if !ok || family != "gemini_live" {
		t.Errorf("gemini must map to gemini_live, got %q/%v", family, ok)
	}
	if family, ok := gatewayServableVoiceProviderType("chatgpt"); ok {
		t.Errorf("chatgpt is reserved, not servable today, got %q", family)
	}
}

func TestLastVoiceProviderProbeInvalid(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		`{"credential_probe":{"status":"invalid"}}`:     true,
		`{"credential_probe":{"status":"ok"}}`:          false,
		`{"credential_probe":{"status":"unreachable"}}`: false,
		`{"credential_probe":{"status":"skipped"}}`:     false,
		`{}`: false,
	}
	for meta, want := range cases {
		vp := db.VoiceProvider{Metadata: []byte(meta)}
		if got := lastVoiceProviderProbeInvalid(vp); got != want {
			t.Errorf("lastVoiceProviderProbeInvalid(%s) = %v, want %v", meta, got, want)
		}
	}
}

func TestAgentVoiceAdvancedRaw(t *testing.T) {
	t.Parallel()

	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`  `)} {
		got, err := agentVoiceAdvancedRaw(raw)
		if err != nil || got != nil {
			t.Errorf("agentVoiceAdvancedRaw(%s) = %q, %v; want nil, nil", raw, got, err)
		}
	}
	got, err := agentVoiceAdvancedRaw(json.RawMessage(`{"temperature":0.5}`))
	if err != nil || string(got) != `{"temperature":0.5}` {
		t.Errorf("object must store as-is, got %q, %v", got, err)
	}
	if _, err := agentVoiceAdvancedRaw(json.RawMessage(`[1,2]`)); err != errVoiceRefAdvancedNotObject {
		t.Errorf("non-object must be rejected, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// DB-backed helpers
// ---------------------------------------------------------------------------

// seedVoiceProvider inserts a pool row in the shared fixture workspace and
// returns its id.
func seedVoiceProvider(t *testing.T, name string, over testutil.Cols) string {
	t.Helper()
	return seedVoiceProviderIn(t, testWorkspaceID, name, over)
}

// seedVoiceProviderIn inserts a pool row in the given workspace. The chain
// enforces same-workspace scope at resolution time, so tests exercising the
// chain must seed the provider into the agent's own workspace.
func seedVoiceProviderIn(t *testing.T, workspaceID, name string, over testutil.Cols) string {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id":  workspaceID,
		"display_name":  name,
		"provider_type": "gemini",
		"enabled":       true,
		"metadata":      testutil.Raw(`'{}'::jsonb`),
	}
	for k, v := range over {
		cols[k] = v
	}
	return dbfx.Insert(t, "voice_provider", cols)
}

// giveProviderCredential stores an encrypted secret under the provider's id
// in the shared runtime_credential store and points credential_ref at it —
// the same two facts the chain (and the credential PUT) establish.
func giveProviderCredential(t *testing.T, box *secretbox.Box, providerID, value string) {
	t.Helper()
	sealed, err := box.Seal([]byte(value))
	if err != nil {
		t.Fatalf("seal credential: %v", err)
	}
	dbfx.Exec(t,
		`INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted) VALUES ($1::uuid, 'api_key', $2)`,
		providerID, sealed)
	dbfx.Exec(t, `UPDATE voice_provider SET credential_ref = $2::text WHERE id = $1::uuid`, providerID, providerID+":api_key")
}

// loadAgentForChain fetches the agent row the chain consumes.
func loadAgentForChain(t *testing.T, agentID string) db.Agent {
	t.Helper()
	agentRow, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent %s: %v", agentID, err)
	}
	return agentRow
}

// providerProbeServer starts an httptest probe target answering the given
// status, wired into the shared handler for the test's lifetime.
func providerProbeServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	previous := testHandler.VoiceProbeBaseURL
	testHandler.VoiceProbeBaseURL = srv.URL
	t.Cleanup(func() {
		srv.Close()
		testHandler.VoiceProbeBaseURL = previous
	})
	return srv
}

// ---------------------------------------------------------------------------
// Resolution chain: the six acceptance branches
// ---------------------------------------------------------------------------

func TestResolveVoiceTarget_AgentOverrideHit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	box := credentialTestBox(t)
	providerID := seedVoiceProvider(t, "override-hit", nil)
	giveProviderCredential(t, box, providerID, "test-key-123")
	agentID := dbfx.Agent(t, "override-hit-agent", "", testutil.Cols{
		"voice_provider_id": providerID,
		"voice_model":       "models/gemini-2.5-flash",
		"voice_advanced":    testutil.Raw(`'{"temperature":0.5}'::jsonb`),
	})

	target, rejection, err := testHandler.resolveVoiceTarget(ctx, loadAgentForChain(t, agentID))
	if err != nil || rejection != nil {
		t.Fatalf("override hit must resolve, got target=%v rejection=%v err=%v", target, rejection, err)
	}
	if target.provider == nil || uuidToString(target.provider.ID) != providerID {
		t.Fatalf("expected the pool provider, got %+v", target)
	}
	if target.configSource != "agent_override" {
		t.Errorf("configSource = %q, want agent_override", target.configSource)
	}
	if target.apiKey != "test-key-123" {
		t.Errorf("decrypted key mismatch: %q", target.apiKey)
	}
	if target.model != "gemini-2.5-flash" {
		t.Errorf("model must lose the models/ prefix, got %q", target.model)
	}
	if target.protocolFamily() != "gemini_live" {
		t.Errorf("protocolFamily = %q, want gemini_live", target.protocolFamily())
	}
	if target.sessionEntityID() != target.provider.ID {
		t.Errorf("chain sessions must record the provider id as the session entity")
	}
	if target.runtime != nil {
		t.Errorf("chain hits never carry a runtime instance")
	}
}

func TestResolveVoiceTarget_WorkspaceDefaultHit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	box := credentialTestBox(t)
	wsID := dbfx.Workspace(t, "ws-default-hit-ws", handlerTestSlug("ws-default-hit"))
	providerID := seedVoiceProviderIn(t, wsID, "ws-default-hit", nil)
	giveProviderCredential(t, box, providerID, "ws-key-456")
	dbfx.Exec(t, `UPDATE workspace SET settings = $2::jsonb WHERE id = $1::uuid`, wsID,
		`{"voice":{"default":{"provider_id":"`+providerID+`","model":"models/gemini-2.5-pro","advanced":{"a":1}}}}`)
	agentID := dbfx.Agent(t, "ws-default-hit-agent", "", testutil.Cols{"workspace_id": wsID})

	target, rejection, err := testHandler.resolveVoiceTarget(ctx, loadAgentForChain(t, agentID))
	if err != nil || rejection != nil {
		t.Fatalf("workspace default must resolve, got rejection=%v err=%v", rejection, err)
	}
	if target.configSource != "workspace_default" {
		t.Errorf("configSource = %q, want workspace_default", target.configSource)
	}
	if target.model != "gemini-2.5-pro" {
		t.Errorf("workspace default model mismatch: %q", target.model)
	}
	if _, ok := target.advanced["a"]; !ok {
		t.Errorf("workspace default advanced must ride along, got %+v", target.advanced)
	}
}

func TestResolveVoiceTarget_BothAbsent_VanillaOrLegacyBridge(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	credentialTestBox(t)

	// No chain config and no legacy binding: the vanilla fallback verdict.
	agentID := dbfx.Agent(t, "chain-absent-agent", "", nil)
	target, rejection, err := testHandler.resolveVoiceTarget(ctx, loadAgentForChain(t, agentID))
	if err != nil || target != nil {
		t.Fatalf("both-absent must return no target, got target=%v err=%v", target, err)
	}
	if rejection == nil || rejection.reason != "voice_unconfigured" {
		t.Fatalf("want voice_unconfigured rejection, got %+v", rejection)
	}

	// Un-migrated legacy binding: the compat bridge resolves it exactly like
	// the pre-D2 gate. A non-voice runtime fails the bridge's capability
	// check — which proves the chain fell through to the bridge at all.
	rtID := dbfx.Runtime(t, "chain-absent-legacy-rt", nil)
	legacyAgentID := dbfx.Agent(t, "chain-absent-legacy-agent", rtID, testutil.Cols{
		"voice_runtime_id": rtID,
	})
	target, rejection, err = testHandler.resolveVoiceTarget(ctx, loadAgentForChain(t, legacyAgentID))
	if err != nil || target != nil {
		t.Fatalf("legacy bridge with a non-voice runtime must reject, got target=%v err=%v", target, err)
	}
	if rejection == nil || rejection.reason != "capability_mismatch" {
		t.Fatalf("want capability_mismatch from the legacy bridge, got %+v", rejection)
	}
}

// The level-wise atomicity pin: a broken override falls back to vanilla and
// must NOT cascade into a healthy workspace default.
func TestResolveVoiceTarget_BrokenOverride_DoesNotCascade(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	box := credentialTestBox(t)
	wsID := dbfx.Workspace(t, "cascade-ws", handlerTestSlug("cascade-ws"))
	healthyID := seedVoiceProviderIn(t, wsID, "cascade-healthy", nil)
	giveProviderCredential(t, box, healthyID, "healthy-key")
	dbfx.Exec(t, `UPDATE workspace SET settings = $2::jsonb WHERE id = $1::uuid`, wsID,
		`{"voice":{"default":{"provider_id":"`+healthyID+`","model":"gemini-2.5-pro"}}}`)
	// Override points at a pool row that has no credential stored.
	keylessID := seedVoiceProviderIn(t, wsID, "cascade-keyless", nil)
	agentID := dbfx.Agent(t, "cascade-agent", "", testutil.Cols{
		"workspace_id":      wsID,
		"voice_provider_id": keylessID,
		"voice_model":       "override-model",
	})

	target, rejection, err := testHandler.resolveVoiceTarget(ctx, loadAgentForChain(t, agentID))
	if err != nil || target != nil {
		t.Fatalf("broken override must not produce a target, got target=%v err=%v", target, err)
	}
	if rejection == nil || rejection.reason != "credential_missing" {
		t.Fatalf("want credential_missing, got %+v", rejection)
	}
	if target != nil && target.configSource == "workspace_default" {
		t.Errorf("chain results must never cascade into the workspace default")
	}
}

// RUYI-619 semantics on pool rows: an "unreachable" verdict never blocks
// initiation — only missing/invalid credentials do.
func TestResolveVoiceTarget_UnreachableProbe_NotBlocking(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	box := credentialTestBox(t)
	providerID := seedVoiceProvider(t, "unreachable-provider", testutil.Cols{
		"metadata": testutil.Raw(`'{"credential_probe":{"status":"unreachable","checked_at":"2026-10-11T00:00:00Z"}}'::jsonb`),
	})
	giveProviderCredential(t, box, providerID, "offline-key")
	agentID := dbfx.Agent(t, "unreachable-agent", "", testutil.Cols{
		"voice_provider_id": providerID,
		"voice_model":       "gemini-2.5-flash",
	})

	target, rejection, err := testHandler.resolveVoiceTarget(ctx, loadAgentForChain(t, agentID))
	if err != nil || rejection != nil {
		t.Fatalf("unreachable verdict must not block, got rejection=%v err=%v", rejection, err)
	}
	if target == nil || target.apiKey != "offline-key" {
		t.Fatalf("expected the key to hand out, got %+v", target)
	}
}

func TestResolveVoiceTarget_DisabledProvider_Rejects(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	box := credentialTestBox(t)
	providerID := seedVoiceProvider(t, "disabled-provider", testutil.Cols{"enabled": false})
	giveProviderCredential(t, box, providerID, "disabled-key")
	agentID := dbfx.Agent(t, "disabled-agent", "", testutil.Cols{"voice_provider_id": providerID})

	target, rejection, err := testHandler.resolveVoiceTarget(ctx, loadAgentForChain(t, agentID))
	if err != nil || target != nil {
		t.Fatalf("disabled provider must reject, got target=%v err=%v", target, err)
	}
	if rejection == nil || rejection.reason != "voice_provider_disabled" {
		t.Fatalf("want voice_provider_disabled, got %+v", rejection)
	}
}

// ---------------------------------------------------------------------------
// Workspace settings write validation
// ---------------------------------------------------------------------------

func TestWorkspaceVoiceSettings_WriteValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	providerID := seedVoiceProvider(t, "settings-validate", nil)
	foreignWs := dbfx.Workspace(t, "settings-validate-foreign", handlerTestSlug("settings-validate-foreign"))
	foreignProviderID := seedVoiceProvider(t, "settings-validate-foreign", testutil.Cols{"workspace_id": foreignWs})

	cases := []struct {
		name    string
		voice   string
		wantErr error
	}{
		{"valid default", `{"default":{"provider_id":"` + providerID + `","model":"m"}}`, nil},
		{"valid empty object", `{}`, nil},
		{"not a document", `3`, errVoiceSettingsMalformed},
		{"unknown provider", `{"default":{"provider_id":"11111111-1111-1111-1111-111111111111"}}`, errVoiceRefProviderUnknown},
		{"foreign provider", `{"default":{"provider_id":"` + foreignProviderID + `"}}`, errVoiceRefProviderForeign},
		{"missing provider_id", `{"default":{"model":"m"}}`, errVoiceRefProviderRequired},
		{"malformed provider_id", `{"default":{"provider_id":"nope"}}`, errVoiceRefProviderInvalid},
	}
	for _, tc := range cases {
		bag := map[string]json.RawMessage{"voice": json.RawMessage(tc.voice)}
		err := testHandler.validateWorkspaceVoiceSettings(ctx, parseUUID(testWorkspaceID), bag)
		if err != tc.wantErr {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.wantErr)
		}
	}
}

// ---------------------------------------------------------------------------
// Credential lifecycle on pool rows: 保存即探针 + rotation resets the badge
// ---------------------------------------------------------------------------

func TestVoiceProviderCredentialLifecycle(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	providerID := seedVoiceProvider(t, "cred-lifecycle", nil)

	putCred := func(value string) *httptest.ResponseRecorder {
		req := newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/voice-providers/"+providerID+"/credentials/api_key", map[string]any{"value": value})
		req = withURLParams(req, "id", testWorkspaceID, "providerId", providerID, "credentialKey", "api_key")
		w := httptest.NewRecorder()
		testHandler.PutVoiceProviderCredential(w, req)
		return w
	}
	badge := func(t *testing.T) (string, string) {
		t.Helper()
		vp, err := testHandler.Queries.GetVoiceProvider(context.Background(), parseUUID(providerID))
		if err != nil {
			t.Fatalf("reload provider: %v", err)
		}
		resp := voiceProviderToResponse(context.Background(), testHandler, vp)
		status := resp.CredentialStatus
		probe := ""
		if resp.Probe != nil {
			probe = resp.Probe.Status
		}
		return status, probe
	}

	// 保存即探针: the very first save probes and records the verdict. A 401
	// from the provider is a key evaluation — the badge says invalid.
	providerProbeServer(t, http.StatusUnauthorized)
	if code := putCred("first-key").Code; code != http.StatusOK {
		t.Fatalf("first PUT must save, got %d", code)
	}
	if status, probe := badge(t); status != "configured" || probe != "invalid" {
		t.Fatalf("after a rejected probe: status=%q probe=%q, want configured/invalid", status, probe)
	}

	// Rotation (a changed connection parameter) re-enters the first
	// connectivity test and the fresh verdict overrides the stale one.
	providerProbeServer(t, http.StatusOK)
	if code := putCred("second-key").Code; code != http.StatusOK {
		t.Fatalf("rotation must save, got %d", code)
	}
	if status, probe := badge(t); status != "configured" || probe != "ok" {
		t.Fatalf("after a passing rotation: status=%q probe=%q, want configured/ok", status, probe)
	}

	// An unreachable probe target is a network conclusion, not a key
	// verdict: the credential is still stored (RUYI-619).
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	previous := testHandler.VoiceProbeBaseURL
	testHandler.VoiceProbeBaseURL = deadURL
	if code := putCred("third-key").Code; code != http.StatusOK {
		t.Fatalf("unreachable probe must not block the save, got %d", code)
	}
	testHandler.VoiceProbeBaseURL = previous
	if status, probe := badge(t); status != "configured" || probe != "unreachable" {
		t.Fatalf("after an unreachable probe: status=%q probe=%q, want configured/unreachable", status, probe)
	}

	// DELETE removes the secret row and the pointer, and clears the badge.
	delReq := withURLParams(newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/voice-providers/"+providerID+"/credentials/api_key", nil),
		"id", testWorkspaceID, "providerId", providerID, "credentialKey", "api_key")
	w := httptest.NewRecorder()
	testHandler.DeleteVoiceProviderCredential(w, delReq)
	if w.Code != http.StatusOK {
		t.Fatalf("credential DELETE failed: %d %s", w.Code, w.Body.String())
	}
	if status, probe := badge(t); status != "not_configured" || probe != "" {
		t.Fatalf("after DELETE: status=%q probe=%q, want not_configured with no badge", status, probe)
	}
}

// ---------------------------------------------------------------------------
// Delete path: unbind everything the chain could dangle on
// ---------------------------------------------------------------------------

func TestDeleteVoiceProvider_UnbindsEverything(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	wsID := dbfx.Workspace(t, "delete-unbind-ws", handlerTestSlug("delete-unbind"))
	providerID := seedVoiceProviderIn(t, wsID, "delete-unbind", nil)
	giveProviderCredential(t, credentialTestBoxGet(t), providerID, "delete-key")
	boundAgent := dbfx.Agent(t, "delete-unbind-agent", "", testutil.Cols{
		"workspace_id":      wsID,
		"voice_provider_id": providerID,
		"voice_model":       "keep-me-not",
	})
	otherAgent := dbfx.Agent(t, "delete-unbind-other", "", nil)
	dbfx.Exec(t, `UPDATE workspace SET settings = $2::jsonb WHERE id = $1::uuid`, wsID,
		`{"voice":{"default":{"provider_id":"`+providerID+`","model":"m"},"other":1}}`)

	req := withURLParams(newRequest(http.MethodDelete, "/api/workspaces/"+wsID+"/voice-providers/"+providerID, nil),
		"id", wsID, "providerId", providerID)
	w := httptest.NewRecorder()
	testHandler.DeleteVoiceProvider(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
	}

	if got := dbfx.Count(t, `SELECT count(*) FROM voice_provider WHERE id = $1::uuid`, providerID); got != 0 {
		t.Errorf("provider row must be gone, count=%d", got)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM runtime_credential WHERE runtime_instance_id = $1::uuid`, providerID); got != 0 {
		t.Errorf("secret rows must be gone, count=%d", got)
	}
	var vpID, vmID pgtype.UUID
	var vAdv []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT voice_provider_id, voice_model, voice_advanced FROM agent WHERE id = $1::uuid`, boundAgent).
		Scan(&vpID, &vmID, &vAdv); err != nil {
		t.Fatalf("reload bound agent: %v", err)
	}
	if vpID.Valid || vmID.Valid || vAdv != nil {
		t.Errorf("bound agent's triple must be fully cleared, got %v/%v/%s", vpID.Valid, vmID.Valid, vAdv)
	}
	var settings []byte
	if err := testPool.QueryRow(context.Background(), `SELECT settings FROM workspace WHERE id = $1::uuid`, wsID).Scan(&settings); err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if strings.Contains(string(settings), providerID) {
		t.Errorf("settings must no longer reference the deleted provider: %s", settings)
	}
	if !strings.Contains(string(settings), `"other"`) {
		t.Errorf("non-voice settings keys must survive: %s", settings)
	}
	_ = otherAgent
}

func TestClearAgentVoiceProviderOverride_ClearsTriple(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	providerID := seedVoiceProvider(t, "clear-triple", nil)
	agentID := dbfx.Agent(t, "clear-triple-agent", "", testutil.Cols{
		"voice_provider_id": providerID,
		"voice_model":       "m",
		"voice_advanced":    testutil.Raw(`'{"a":1}'::jsonb`),
	})

	if _, err := testHandler.Queries.ClearAgentVoiceProviderOverride(context.Background(), parseUUID(agentID)); err != nil {
		t.Fatalf("clear: %v", err)
	}
	var vpID, vmID pgtype.UUID
	var vAdv []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT voice_provider_id, voice_model, voice_advanced FROM agent WHERE id = $1::uuid`, agentID).
		Scan(&vpID, &vmID, &vAdv); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if vpID.Valid || vmID.Valid || vAdv != nil {
		t.Errorf("triple must be fully cleared, got %v/%v/%s", vpID.Valid, vmID.Valid, vAdv)
	}
}

// credentialTestBoxGet returns the box credentialTestBox most recently
// installed on the shared handler.
func credentialTestBoxGet(t *testing.T) *secretbox.Box {
	t.Helper()
	if testHandler.RuntimeCredentialBox == nil {
		t.Fatalf("no credential box installed; call credentialTestBox first")
	}
	return testHandler.RuntimeCredentialBox
}

// ---------------------------------------------------------------------------
// Migration 961: replay the DML over freshly seeded legacy instances
// ---------------------------------------------------------------------------

// TestMigration961_ReplayOverLegacyInstances executes migration 961's three
// DML statements against rows seeded in the post-migration schema, proving
// the seeding path the存量 fleet already went through: pool rows mirror the
// instance's disabled flag, credentials are copied as ciphertext, bound
// agents get the override triple backfilled with the models/ prefix
// stripped. The shared dev database holds no manual gemini_live instances
// (asserted below); the test skips rather than replaying DML over a fleet
// it does not own.
func TestMigration961_ReplayOverLegacyInstances(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var preexisting int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_runtime WHERE registration_source = 'manual' AND provider = 'gemini_live'`).
		Scan(&preexisting); err != nil {
		t.Fatalf("count legacy instances: %v", err)
	}
	if preexisting != 0 {
		t.Skipf("shared database holds %d manual gemini_live instances; replaying 961 DML would project rows this test does not own", preexisting)
	}

	box := credentialTestBox(t)
	seedLegacy := func(name string, disabled bool) string {
		meta := `{"model":"models/gemini-2.5-flash","advanced":{"temperature":0.5}`
		if disabled {
			meta += `,"disabled":true`
		}
		meta += `}`
		rtID := dbfx.Runtime(t, name, testutil.Cols{
			"provider":            "gemini_live",
			"registration_source": "manual",
			"metadata":            testutil.Raw(`'` + meta + `'::jsonb`),
		})
		sealed, err := box.Seal([]byte("legacy-key-" + name))
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		dbfx.Exec(t, `INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted) VALUES ($1::uuid, 'api_key', $2)`, rtID, sealed)
		dbfx.Exec(t, `UPDATE agent_runtime SET credential_ref = $2::text WHERE id = $1::uuid`, rtID, rtID+":api_key")
		return dbfx.Agent(t, name+"-agent", rtID, testutil.Cols{"voice_runtime_id": rtID})
	}
	healthyAgent := seedLegacy("m961-healthy", false)
	disabledAgent := seedLegacy("m961-disabled", true)
	healthyRt := legacyRuntimeID(t, healthyAgent)
	disabledRt := legacyRuntimeID(t, disabledAgent)

	run961DML(t)

	assertProjection := func(agentID, rtID string, wantEnabled bool) {
		t.Helper()
		var providerID string
		var enabled bool
		if err := testPool.QueryRow(ctx,
			`SELECT id::text, enabled FROM voice_provider WHERE source_runtime_id = $1::uuid`, rtID).
			Scan(&providerID, &enabled); err != nil {
			t.Fatalf("pool row for %s: %v", rtID, err)
		}
		if enabled != wantEnabled {
			t.Errorf("pool row enabled = %v, want %v (disabled mirror)", enabled, wantEnabled)
		}
		if got := dbfx.Count(t, `SELECT count(*) FROM runtime_credential WHERE runtime_instance_id = $1::uuid`, providerID); got != 1 {
			t.Errorf("credential copy count = %d, want 1", got)
		}
		var vpID pgtype.UUID
		var model string
		var advanced []byte
		if err := testPool.QueryRow(ctx,
			`SELECT voice_provider_id, voice_model, voice_advanced FROM agent WHERE id = $1::uuid`, agentID).
			Scan(&vpID, &model, &advanced); err != nil {
			t.Fatalf("reload agent %s: %v", agentID, err)
		}
		if uuidToString(vpID) != providerID {
			t.Errorf("agent override provider = %s, want %s", uuidToString(vpID), providerID)
		}
		if model != "gemini-2.5-flash" {
			t.Errorf("backfilled model = %q, want the models/ prefix stripped", model)
		}
		if !strings.Contains(string(advanced), `"temperature"`) {
			t.Errorf("backfilled advanced mismatch: %s", advanced)
		}
	}
	assertProjection(healthyAgent, healthyRt, true)
	assertProjection(disabledAgent, disabledRt, false)
}

// legacyRuntimeID reads the voice_runtime_id binding off a seeded agent.
func legacyRuntimeID(t *testing.T, agentID string) string {
	t.Helper()
	var rtID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT voice_runtime_id::text FROM agent WHERE id = $1::uuid`, agentID).Scan(&rtID); err != nil {
		t.Fatalf("read legacy binding: %v", err)
	}
	return rtID
}

// migrationDMLStatements reads a migration file and returns only its INSERT
// and UPDATE statements — the DML tail a post-migration replay re-projects.
func migrationDMLStatements(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var codeLines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		codeLines = append(codeLines, line)
	}
	var out []string
	for _, stmt := range strings.Split(strings.Join(codeLines, "\n"), ";") {
		trimmed := strings.TrimSpace(stmt)
		if strings.HasPrefix(trimmed, "INSERT INTO") || strings.HasPrefix(trimmed, "UPDATE") {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

// run961DML executes the DML statements (the INSERT/UPDATE tail) of
// migration 961 against the current database. Structural statements are
// already applied; replaying only the DML is what the test wants: the
// seeding path re-projects the freshly seeded legacy rows.
func run961DML(t *testing.T) {
	t.Helper()
	stmts, err := migrationDMLStatements("../../migrations/961_voice_provider_config.up.sql")
	if err != nil {
		t.Fatalf("read 961 up.sql: %v", err)
	}
	for _, stmt := range stmts {
		if _, err := testPool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("replay 961 DML: %v\nstatement: %.120s", err, stmt)
		}
	}
}
