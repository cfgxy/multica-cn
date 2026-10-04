package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TestCreateRuntimeProfile_VoiceFamilyContract pins the gemini_live profile
// contract (§4.4): a voice family is created without a command_name (it is
// API-backed, there is no CLI to resolve), and the server derives the
// capabilities column from the family baseline — clients read it back on the
// profile, they do not supply it.
func TestCreateRuntimeProfile_VoiceFamilyContract(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/runtime-profiles", map[string]any{
		"display_name":    "Gemini Live Profile",
		"protocol_family": "gemini_live",
	})
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.CreateRuntimeProfile(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp RuntimeProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM runtime_profile WHERE id = $1`, resp.ID)
	})

	wantCaps := agent.Capabilities{Text: false, RealtimeVoice: true, Tools: true}
	if resp.Capabilities != wantCaps {
		t.Errorf("response capabilities = %+v, want %+v (family baseline)", resp.Capabilities, wantCaps)
	}

	var dbCapsRaw string
	if err := testPool.QueryRow(ctx,
		`SELECT capabilities::text FROM runtime_profile WHERE id = $1`, resp.ID,
	).Scan(&dbCapsRaw); err != nil {
		t.Fatalf("read capabilities column: %v", err)
	}
	// jsonb does not preserve key order — decode and compare the declaration.
	var dbCaps agent.Capabilities
	if err := json.Unmarshal([]byte(dbCapsRaw), &dbCaps); err != nil {
		t.Fatalf("decode DB capabilities %s: %v", dbCapsRaw, err)
	}
	if dbCaps != wantCaps {
		t.Errorf("DB capabilities = %+v, want the explicit family baseline %+v", dbCaps, wantCaps)
	}
}

// TestCreateRuntimeProfile_VoiceFamilyRejectsCommandName: a voice profile
// with a command_name would promise the daemon something it can never
// resolve; the API refuses the combination outright.
func TestCreateRuntimeProfile_VoiceFamilyRejectsCommandName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/runtime-profiles", map[string]any{
		"display_name":    "Gemini Live With Command",
		"protocol_family": "gemini_live",
		"command_name":    "gemini-live-cli",
	})
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.CreateRuntimeProfile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDaemonListRuntimeProfiles_SkipsVoiceFamilies: voice profiles are
// API-backed and daemon-invisible (§4.4). The daemon's profile pull must
// return only CLI profiles even when a voice profile exists in the
// workspace — an old daemon that saw one would try to resolve a
// command_name it can never have.
func TestDaemonListRuntimeProfiles_SkipsVoiceFamilies(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	cliProfileID := insertRuntimeProfileFixture(t, ctx, "Daemon Visible CLI", "kimi", "kimi")
	insertRuntimeProfileFixture(t, ctx, "Daemon Invisible Voice", "gemini_live", "")

	// No daemon token in play: the caller rides the member fallback inside
	// requireDaemonWorkspaceAccess (testUserID owns the workspace), which is
	// the same profile-list path a PAT-authed daemon takes.
	req := newRequestAsUser(testUserID, http.MethodGet, "/api/daemon/workspaces/"+testWorkspaceID+"/runtime-profiles", nil)
	req = withURLParam(req, "workspaceId", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.DaemonListRuntimeProfiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		WorkspaceID     string                   `json:"workspace_id"`
		RuntimeProfiles []RuntimeProfileResponse `json:"runtime_profiles"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	sawCLI := false
	for _, p := range resp.RuntimeProfiles {
		if p.ID == cliProfileID {
			sawCLI = true
		}
		if agent.IsVoiceProtocolFamily(p.ProtocolFamily) {
			t.Errorf("daemon pull leaked voice profile %s (%s)", p.ID, p.ProtocolFamily)
		}
	}
	if !sawCLI {
		t.Errorf("daemon pull lost the CLI profile %s — the filter over-pruned", cliProfileID)
	}
}
