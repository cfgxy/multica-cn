package handler

// RUYI-425 stage 3: the manual instance registration API (§4.3 create path).
// All values are fake; no provider is contacted — the key never appears in
// this API at all (creation stores no credential; the form PUTs it to the
// stage-2 credential endpoint afterwards, which probes against an injected
// base URL).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/agent"
)

type manualRuntimeCase struct {
	name     string
	profile  string
	model    string
	advanced any
	wantMsg  string
}

func postManualRuntime(t *testing.T, instanceName string, tc manualRuntimeCase) *testutil.Response {
	t.Helper()
	req := CreateManualRuntimeRequest{Name: instanceName, ProfileID: tc.profile, Model: tc.model}
	if tc.advanced != nil {
		raw, err := json.Marshal(tc.advanced)
		if err != nil {
			t.Fatalf("marshal advanced: %v", err)
		}
		req.Advanced = raw
	}
	return testutil.Call(t, testHandler.CreateManualRuntime,
		newRequest(http.MethodPost, "/api/runtimes", req),
	)
}

// TestCreateManualRuntime_HappyPathAndDuplicates pins the §4.3 create
// contract: duplicate names are allowed (Owner decision 2), the row is born
// manual/online/public with daemon_id NULL (which structurally excludes it
// from every daemon upsert conflict target), capabilities are derived
// server-side from the profile's Type-layer declaration, and model/advanced
// land in metadata exactly where the §4.3 settings card reads them.
func TestCreateManualRuntime_HappyPathAndDuplicates(t *testing.T) {
	ctx := context.Background()
	profileID := insertRuntimeProfileFixture(t, ctx, "Gemini Live Create", "gemini_live", "")

	first := postManualRuntime(t, "Gemini Live-个人账号", manualRuntimeCase{
		profile:  profileID,
		model:    "gemini-3.8-live",
		advanced: map[string]any{"region": "asia-east1"},
	})
	first.Want(http.StatusCreated)
	var created AgentRuntimeResponse
	if err := json.NewDecoder(first.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, created.ID)
	})

	if created.RegistrationSource != "manual" {
		t.Errorf("registration_source = %q, want 'manual'", created.RegistrationSource)
	}
	if created.Status != "online" {
		t.Errorf("status = %q, want 'online' (API-backed instances are born active)", created.Status)
	}
	if created.ProtocolFamily != "gemini_live" {
		t.Errorf("protocol_family = %q, want 'gemini_live'", created.ProtocolFamily)
	}
	if !created.Capabilities.RealtimeVoice || created.Capabilities.Text {
		t.Errorf("capabilities = %+v, want server-derived {realtime_voice:true, text:false} (§4.4)", created.Capabilities)
	}
	if created.Visibility != "public" {
		t.Errorf("visibility = %q, want 'public' (§4.3 v1 fixes workspace-wide visibility)", created.Visibility)
	}

	// Row shape: daemon_id NULL + the metadata bag the settings card reads.
	var daemonID *string
	var metadataRaw string
	if err := testPool.QueryRow(ctx, `
		SELECT daemon_id, metadata::text FROM agent_runtime WHERE id = $1
	`, created.ID).Scan(&daemonID, &metadataRaw); err != nil {
		t.Fatalf("read created row: %v", err)
	}
	if daemonID != nil {
		t.Errorf("daemon_id = %v, want NULL (manual instances have no daemon)", *daemonID)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataRaw), &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata["model"] != "gemini-3.8-live" {
		t.Errorf("metadata.model = %v, want 'gemini-3.8-live'", metadata["model"])
	}
	adv, ok := metadata["advanced"].(map[string]any)
	if !ok || adv["region"] != "asia-east1" {
		t.Errorf("metadata.advanced = %v, want {region: asia-east1}", metadata["advanced"])
	}

	// Duplicate name: §4.3 allows it, no uniqueness constraint — a second
	// create with the same display name must succeed with a distinct id.
	second := postManualRuntime(t, "Gemini Live-个人账号", manualRuntimeCase{profile: profileID})
	second.Want(http.StatusCreated)
	var dup AgentRuntimeResponse
	if err := json.NewDecoder(second.Body).Decode(&dup); err != nil {
		t.Fatalf("decode duplicate create response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, dup.ID)
	})
	if dup.ID == created.ID {
		t.Fatal("duplicate-name create returned the same instance id, want a distinct row")
	}

	// Omittable fields stay unset: no model key means the §4.3 form shows
	// its placeholder default rather than an empty override.
	third := postManualRuntime(t, "Gemini Live-最小字段", manualRuntimeCase{profile: profileID})
	third.Want(http.StatusCreated)
	var minimal AgentRuntimeResponse
	if err := json.NewDecoder(third.Body).Decode(&minimal); err != nil {
		t.Fatalf("decode minimal create response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, minimal.ID)
	})
	var minimalMetadata map[string]any
	var minimalRaw string
	if err := testPool.QueryRow(ctx, `SELECT metadata::text FROM agent_runtime WHERE id = $1`, minimal.ID).
		Scan(&minimalRaw); err != nil {
		t.Fatalf("read minimal row: %v", err)
	}
	if err := json.Unmarshal([]byte(minimalRaw), &minimalMetadata); err != nil {
		t.Fatalf("decode minimal metadata: %v", err)
	}
	if _, has := minimalMetadata["model"]; has {
		t.Errorf("metadata.model = %v, want absent so the placeholder default applies", minimalMetadata["model"])
	}
	if _, has := minimalMetadata["advanced"]; has {
		t.Errorf("metadata.advanced = %v, want absent (omitted means unset, never null)", minimalMetadata["advanced"])
	}
}

// TestCreateManualRuntime_Rejections covers the negative contract: empty
// name, unknown profile, CLI-family profile (daemon-born only), bare-null or
// non-object advanced, oversized model — all 400 with nothing inserted.
func TestCreateManualRuntime_Rejections(t *testing.T) {
	ctx := context.Background()
	voiceProfileID := insertRuntimeProfileFixture(t, ctx, "Gemini Live Rej", "gemini_live", "")
	cliProfileID := insertRuntimeProfileFixture(t, ctx, "Claude CLI Rej", "claude", "claude")

	cases := []struct {
		label    string
		instance string // the instance display name under test
		tc       manualRuntimeCase
	}{
		{"empty name", "", manualRuntimeCase{profile: voiceProfileID, wantMsg: "name is required"}},
		{"blank profile", "Rejection Probe", manualRuntimeCase{wantMsg: "invalid profile_id"}},
		{"unknown profile", "Rejection Probe", manualRuntimeCase{profile: "00000000-0000-0000-0000-0000000000d1", wantMsg: "invalid profile_id"}},
		{"cli family profile", "Rejection Probe", manualRuntimeCase{profile: cliProfileID, wantMsg: "manual registration only supports voice protocol families"}},
		{"non-object advanced", "Rejection Probe", manualRuntimeCase{profile: voiceProfileID, advanced: []any{"not", "an object"}, wantMsg: "advanced must be a JSON object"}},
		{"bare-null advanced", "Rejection Probe", manualRuntimeCase{profile: voiceProfileID, advanced: json.RawMessage("null"), wantMsg: "advanced must be a JSON object"}},
		{"oversized model", "Rejection Probe", manualRuntimeCase{profile: voiceProfileID, model: strings.Repeat("m", maxRuntimeModelLen+1), wantMsg: "model is too long"}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			w := postManualRuntime(t, c.instance, c.tc)
			w.Want(http.StatusBadRequest)
			var got struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if !strings.Contains(got.Error, c.tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", got.Error, c.tc.wantMsg)
			}
		})
	}

	// Nothing from the rejection round leaked into the instance table.
	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_runtime WHERE name = 'Rejection Probe'
	`).Scan(&count); err != nil {
		t.Fatalf("count leaked rows: %v", err)
	}
	if count != 0 {
		t.Errorf("rejection round inserted %d rows, want 0 (validate before mutate)", count)
	}
}

// TestManualInstances_StructurallyExcludedFromDaemonProbing pins criterion 1d
// (§4.4 rule 4). The daemon only probes CLI binaries behind runtime profiles
// with a command_name, and the voice family is API-enforced command-less at
// the profile gate — so a gemini_live profile can never be picked up by the
// daemon's profile sync (its first filter skips CommandName == "" rows before
// any binary probing), and manual instances carry no daemon identity for any
// daemon upsert conflict target to match.
func TestManualInstances_StructurallyExcludedFromDaemonProbing(t *testing.T) {
	if !agent.IsVoiceProtocolFamily("gemini_live") {
		t.Fatal("gemini_live lost its voice-family classification")
	}

	// The load-bearing invariant: the profile API refuses command-carrying
	// voice profiles (stage-1 rule), so the daemon's CommandName == ""
	// skip filter is unreachable-proof for the voice family.
	profileW := testutil.Call(t, testHandler.CreateRuntimeProfile, newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/runtime-profiles", map[string]any{
		"display_name":    "Gemini Live With Command",
		"protocol_family": "gemini_live",
		"command_name":    "gemini",
	}))
	profileW.Want(http.StatusBadRequest)

	ctx := context.Background()
	profileID := insertRuntimeProfileFixture(t, ctx, "Gemini Live Skip", "gemini_live", "")
	w := postManualRuntime(t, "Gemini Live-Skip", manualRuntimeCase{profile: profileID})
	w.Want(http.StatusCreated)
	var created AgentRuntimeResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, created.ID)
	})

	var daemonID *string
	var registrationSource string
	if err := testPool.QueryRow(ctx, `
		SELECT daemon_id, registration_source FROM agent_runtime WHERE id = $1
	`, created.ID).Scan(&daemonID, &registrationSource); err != nil {
		t.Fatalf("read created row: %v", err)
	}
	if daemonID != nil {
		t.Errorf("daemon_id = %q, want NULL — a manual row must not carry daemon identity", *daemonID)
	}
	if registrationSource != "manual" {
		t.Errorf("registration_source = %q, want 'manual'", registrationSource)
	}
}
