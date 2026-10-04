package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// insertVoiceFamilyFixture creates a gemini_live profile + instance pair and
// returns (profileID, instanceID), both registered for cleanup. The instance
// is what a §4.5 credential or a voice slot binds to.
func insertVoiceFamilyFixture(t *testing.T, ctx context.Context, name string) (string, string) {
	t.Helper()
	profileID := insertRuntimeProfileFixture(t, ctx, name+" Profile", "gemini_live", "")
	instanceID := insertProfileRuntimeFixture(t, ctx, profileID, name+" Runtime", "gemini_live")
	return profileID, instanceID
}

// TestCreateAgent_VoiceSlotBinding pins the happy path of the dual-slot
// design (§7.1): one request binds a text CLI instance and a gemini_live
// instance, the response exposes both, and the DB row carries the voice
// binding. The legacy runtime_id request field keeps meaning exactly what it
// meant before RUYI-425 — the text slot (§7.2).
func TestCreateAgent_VoiceSlotBinding(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	textProfileID := insertRuntimeProfileFixture(t, ctx, "Dual Slot Text Profile", "kimi", "kimi")
	textInstanceID := insertProfileRuntimeFixture(t, ctx, textProfileID, "Dual Slot Text Runtime", "kimi")
	_, voiceInstanceID := insertVoiceFamilyFixture(t, ctx, "Dual Slot Voice")

	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":             "Dual Slot Agent",
		"runtime_id":       textInstanceID,
		"voice_runtime_id": voiceInstanceID,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, resp.ID)
	})

	if resp.RuntimeID != textInstanceID {
		t.Errorf("runtime_id = %q, want the text instance %q", resp.RuntimeID, textInstanceID)
	}
	if resp.VoiceRuntimeID != voiceInstanceID {
		t.Errorf("voice_runtime_id = %q, want the voice instance %q", resp.VoiceRuntimeID, voiceInstanceID)
	}
	if !resp.VoiceRuntimeBound {
		t.Error("voice_runtime_bound = false, want true")
	}

	var dbVoice *string
	if err := testPool.QueryRow(ctx,
		`SELECT voice_runtime_id::text FROM agent WHERE id = $1`, resp.ID,
	).Scan(&dbVoice); err != nil {
		t.Fatalf("read agent voice slot: %v", err)
	}
	if dbVoice == nil || *dbVoice != voiceInstanceID {
		t.Errorf("DB voice_runtime_id = %v, want %q", dbVoice, voiceInstanceID)
	}
}

// TestCreateAgent_VoiceSlotRejectsCLIFamily: §4.4 rule 1 — a text CLI family
// does not declare realtime_voice, so it can never occupy the voice slot.
func TestCreateAgent_VoiceSlotRejectsCLIFamily(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	profileID := insertRuntimeProfileFixture(t, ctx, "Voice Gate CLI Profile", "kimi", "kimi")
	textInstanceID := insertProfileRuntimeFixture(t, ctx, profileID, "Voice Gate CLI Text", "kimi")
	cliTargetID := insertProfileRuntimeFixture(t, ctx, profileID, "Voice Gate CLI Target", "kimi")

	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":             "Voice Gate CLI Agent",
		"runtime_id":       textInstanceID,
		"voice_runtime_id": cliTargetID,
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "realtime_voice") {
		t.Errorf("error should name the missing capability: %s", w.Body.String())
	}

	var bound int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent WHERE voice_runtime_id = $1`, cliTargetID,
	).Scan(&bound); err != nil {
		t.Fatalf("count voice bindings: %v", err)
	}
	if bound != 0 {
		t.Errorf("rejected request left %d agents bound to the CLI instance", bound)
	}
}

// TestCreateAgent_VoiceSlotRejectsSameInstance: one instance cannot occupy
// both slots of the same agent — the slots resolve to different runtimes by
// definition (§7.1).
func TestCreateAgent_VoiceSlotRejectsSameInstance(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	profileID := insertRuntimeProfileFixture(t, ctx, "Same Slot Profile", "kimi", "kimi")
	instanceID := insertProfileRuntimeFixture(t, ctx, profileID, "Same Slot Runtime", "kimi")

	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":             "Same Slot Agent",
		"runtime_id":       instanceID,
		"voice_runtime_id": instanceID,
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "must differ") {
		t.Errorf("error should explain the same-instance rule: %s", w.Body.String())
	}
}

// TestCreateAgent_TextSlotRejectsVoiceFamily: §4.4 rule 2 — gemini_live
// declares no text capability, so it cannot be the runtime executing text
// tasks, on create or (see the update twin) on rebind.
func TestCreateAgent_TextSlotRejectsVoiceFamily(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	_, voiceInstanceID := insertVoiceFamilyFixture(t, ctx, "Text Gate Voice")

	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":       "Text Gate Agent",
		"runtime_id": voiceInstanceID,
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot be used as the text runtime") {
		t.Errorf("error should name the text-slot rejection: %s", w.Body.String())
	}
}

// TestUpdateAgent_VoiceSlotTriState pins the tri-state contract: omitted
// preserves, "" clears, an instance id binds. The clear must persist through
// a later omitted-field update — COALESCE preserve must not resurrect a
// cleared slot.
func TestUpdateAgent_VoiceSlotTriState(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	textProfileID := insertRuntimeProfileFixture(t, ctx, "Tri State Text Profile", "kimi", "kimi")
	textInstanceID := insertProfileRuntimeFixture(t, ctx, textProfileID, "Tri State Text Runtime", "kimi")
	_, voiceInstanceID := insertVoiceFamilyFixture(t, ctx, "Tri State Voice")
	agentID := createCascadeFixtureAgent(t, ctx, textInstanceID, "Tri State Agent")

	put := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.UpdateAgent(w, withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, body), "id", agentID))
		return w
	}

	// Bind.
	if w := put(map[string]any{"voice_runtime_id": voiceInstanceID}); w.Code != http.StatusOK {
		t.Fatalf("bind voice slot: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var bound *string
	if err := testPool.QueryRow(ctx,
		`SELECT voice_runtime_id::text FROM agent WHERE id = $1`, agentID,
	).Scan(&bound); err != nil {
		t.Fatalf("read voice slot after bind: %v", err)
	}
	if bound == nil || *bound != voiceInstanceID {
		t.Fatalf("voice slot after bind = %v, want %q", bound, voiceInstanceID)
	}

	// Clear with "".
	if w := put(map[string]any{"voice_runtime_id": ""}); w.Code != http.StatusOK {
		t.Fatalf("clear voice slot: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cleared *string
	if err := testPool.QueryRow(ctx,
		`SELECT voice_runtime_id::text FROM agent WHERE id = $1`, agentID,
	).Scan(&cleared); err != nil {
		t.Fatalf("read voice slot after clear: %v", err)
	}
	if cleared != nil {
		t.Fatalf("voice slot after clear = %q, want NULL", *cleared)
	}

	// Omit — the cleared state survives the COALESCE preserve.
	if w := put(map[string]any{"description": "touch another field"}); w.Code != http.StatusOK {
		t.Fatalf("omit voice slot: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var stillCleared *string
	if err := testPool.QueryRow(ctx,
		`SELECT voice_runtime_id::text FROM agent WHERE id = $1`, agentID,
	).Scan(&stillCleared); err != nil {
		t.Fatalf("read voice slot after omit: %v", err)
	}
	if stillCleared != nil {
		t.Fatalf("voice slot after omitted-field update = %q, want NULL (preserve must not resurrect a cleared slot)", *stillCleared)
	}
}

// TestUpdateAgent_TextSlotRejectsVoiceFamily: the §4.4 rule 2 gate holds on
// rebind too — UpdateAgent must not be the quiet path around CreateAgent.
func TestUpdateAgent_TextSlotRejectsVoiceFamily(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	textProfileID := insertRuntimeProfileFixture(t, ctx, "Rebind Gate Text Profile", "kimi", "kimi")
	textInstanceID := insertProfileRuntimeFixture(t, ctx, textProfileID, "Rebind Gate Text Runtime", "kimi")
	_, voiceInstanceID := insertVoiceFamilyFixture(t, ctx, "Rebind Gate Voice")
	agentID := createCascadeFixtureAgent(t, ctx, textInstanceID, "Rebind Gate Agent")

	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"runtime_id": voiceInstanceID,
	}), "id", agentID))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cannot be used as the text runtime") {
		t.Errorf("error should name the text-slot rejection: %s", w.Body.String())
	}

	var rebound int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent WHERE id = $1 AND runtime_id = $2`, agentID, voiceInstanceID,
	).Scan(&rebound); err != nil {
		t.Fatalf("count rebound agents: %v", err)
	}
	if rebound != 0 {
		t.Error("rejected rebind moved the agent onto the voice instance")
	}
}

// TestAgentResponseVoiceSlotJSONNames: the wire names are the dual-slot
// contract clients code against (§7.1); a rename would silently unbind every
// voice UI toggle.
func TestAgentResponseVoiceSlotJSONNames(t *testing.T) {
	resp := AgentResponse{VoiceRuntimeID: "abc", VoiceRuntimeBound: true}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := fields["voice_runtime_id"]; !ok {
		t.Error("AgentResponse is missing the voice_runtime_id JSON field")
	}
	if _, ok := fields["voice_runtime_bound"]; !ok {
		t.Error("AgentResponse is missing the voice_runtime_bound JSON field")
	}
	if _, ok := fields["runtime_id"]; !ok {
		t.Error("AgentResponse is missing the runtime_id JSON field (the text slot)")
	}
}

// Compile-time guard: the handler gates consume the shared capability
// vocabulary, so a rename in pkg/agent breaks here instead of silently
// diverging at the API edge.
var _ = agent.SlotText
