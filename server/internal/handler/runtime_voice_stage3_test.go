package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// RUYI-425 stage 3: the binding-gate closure, the voice-session initiation
// gate (§4.4 rule 3), and the WSS relay. Providers are always stubbed
// in-process servers and only fake key values are used throughout — no real
// Gemini endpoint is ever contacted.

const voiceTestAPIKey = "fake-gemini-key-stage3-not-a-real-credential"

// insertOnlineVoiceInstanceFixture registers a manual gemini_live instance in
// the active state the §4.3 form produces (the §4.4 gate's happy input).
func insertOnlineVoiceInstanceFixture(t *testing.T, name, metadata string) string {
	t.Helper()
	ctx := context.Background()
	profileID := insertRuntimeProfileFixture(t, ctx, name+" Profile", "gemini_live", "")
	if metadata == "" {
		metadata = "{}"
	}
	return dbfx.Runtime(t, name, testutil.Cols{
		"provider":            "gemini_live",
		"registration_source": "manual",
		"profile_id":          profileID,
		"metadata":            testutil.Raw("'" + metadata + "'::jsonb"),
		"visibility":          "public",
	})
}

// stubVoiceProvider is an in-process BidiGenerateContent peer: it records the
// gateway's handshake and setup frame, answers with setupComplete, hands the
// live connection to the test for downstream frames, and records what the
// relay forwards upstream.
type stubVoiceProvider struct {
	URL string // ws:// origin for VoiceProviderWSBaseURL

	mu         sync.Mutex
	authHeader string
	urlQuery   string
	setups     []map[string]any
	clientMsgs [][]byte

	conns   chan *websocket.Conn
	writeMu sync.Mutex // serialises every stub→gateway write
}

func newStubVoiceProvider(t *testing.T) *stubVoiceProvider {
	t.Helper()
	p := &stubVoiceProvider{conns: make(chan *websocket.Conn, 4)}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.authHeader = r.Header.Get("x-goog-api-key")
		p.urlQuery = r.URL.RawQuery
		p.mu.Unlock()

		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		select {
		case p.conns <- conn:
		default:
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame map[string]json.RawMessage
			isSetup := json.Unmarshal(data, &frame) == nil && frame["setup"] != nil
			p.mu.Lock()
			if isSetup {
				var decoded map[string]any
				json.Unmarshal(data, &decoded)
				p.setups = append(p.setups, decoded)
			} else {
				p.clientMsgs = append(p.clientMsgs, data)
			}
			p.mu.Unlock()
			if isSetup {
				p.sendDownstream(conn, map[string]any{"setupComplete": map[string]any{}})
			}
		}
	}))
	t.Cleanup(srv.Close)
	p.URL = "ws" + strings.TrimPrefix(srv.URL, "http")

	previous := testHandler.VoiceProviderWSBaseURL
	testHandler.VoiceProviderWSBaseURL = p.URL
	t.Cleanup(func() { testHandler.VoiceProviderWSBaseURL = previous })
	return p
}

// sendDownstream writes a stub→gateway frame under the write lock.
func (p *stubVoiceProvider) sendDownstream(conn *websocket.Conn, frame map[string]any) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.WriteJSON(frame)
}

// gatewayConn returns the live stub-side connection to the gateway.
func (p *stubVoiceProvider) gatewayConn(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-p.conns:
		return conn
	case <-time.After(3 * time.Second):
		t.Fatalf("the gateway never dialed the stub provider")
		return nil
	}
}

func (p *stubVoiceProvider) handshake(t *testing.T) (auth, query string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authHeader, p.urlQuery
}

func (p *stubVoiceProvider) setupsRecorded(t *testing.T) []map[string]any {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.setups) == 0 {
		t.Fatalf("the gateway never sent a setup frame")
	}
	return p.setups
}

// voiceAuthCookie mints a signed session JWT for userID — the same token
// shape the Auth middleware issues — wrapped as the auth cookie. RUYI-449:
// the voice route no longer trusts X-User-ID (it sits outside the Auth
// group, where the header would be client-controlled), so tests present
// real credentials like production clients do.
func voiceAuthCookie(t *testing.T, userID string) *http.Cookie {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatalf("mint voice session token: %v", err)
	}
	return &http.Cookie{Name: auth.AuthCookieName, Value: signed}
}

// startVoiceSession issues the plain-HTTP initiation request with cookie
// identity (the browser/desktop path).
func startVoiceSession(t *testing.T, agentID string) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/"+agentID+"/voice-session", nil), "id", agentID)
	req.Header.Del("X-User-ID")
	req.AddCookie(voiceAuthCookie(t, testUserID))
	w := httptest.NewRecorder()
	testHandler.StartVoiceSession(w, req)
	return w
}

// expectVoiceUnavailable asserts the §4.4 rule-3 wire contract.
func expectVoiceUnavailable(t *testing.T, entrypoint string, w *httptest.ResponseRecorder, wantReason string) {
	t.Helper()
	if w.Code != http.StatusConflict {
		t.Fatalf("%s: expected 409, got %d: %s", entrypoint, w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode rejection: %v", entrypoint, err)
	}
	if body["code"] != "VOICE_UNAVAILABLE:"+wantReason {
		t.Fatalf("%s: code = %q, want %q (body %s)", entrypoint, body["code"], "VOICE_UNAVAILABLE:"+wantReason, w.Body.String())
	}
}

// insertVoiceBoundAgentFixture inserts an agent whose voice slot points at a
// gemini_live instance. instanceID "" leaves the voice slot empty.
func insertVoiceBoundAgentFixture(t *testing.T, name, instanceID string) string {
	t.Helper()
	cols := testutil.Cols{"instructions": "VOICE-CONTEXT-MARKER-7q2: follow the injected instructions"}
	if instanceID != "" {
		cols["voice_runtime_id"] = instanceID
	}
	return dbfx.Agent(t, name, handlerTestRuntimeID(t), cols)
}

// seedUsableVoiceSetup produces the full gate happy path: an online manual
// instance with a probed-valid credential and an agent bound to it.
func seedUsableVoiceSetup(t *testing.T, name, metadata string) (agentID, instanceID string) {
	t.Helper()
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)
	instanceID = insertOnlineVoiceInstanceFixture(t, name, metadata)
	if w := putRuntimeCredential(t, instanceID, "api_key", voiceTestAPIKey); w.Code != http.StatusOK {
		t.Fatalf("seed credential: %d %s", w.Code, w.Body.String())
	}
	agentID = insertVoiceBoundAgentFixture(t, name+" Agent", instanceID)
	return agentID, instanceID
}

// TestVoiceSessionGate_VoiceUnavailableRejections pins every §4.4 rule-3
// rejection: session start re-checks the binding-time facts and refuses with
// a stable VOICE_UNAVAILABLE:<reason> code before any websocket upgrade.
// The disabled case is the 决策 3 deadline item — the flag the §4.3 toggle
// writes must gate here.
func TestVoiceSessionGate_VoiceUnavailableRejections(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	credentialTestBox(t)
	stubProbeTarget(t, http.StatusOK)

	t.Run("no_voice_runtime", func(t *testing.T) {
		agentID := dbfx.Agent(t, "Gate No Voice Agent", handlerTestRuntimeID(t))
		expectVoiceUnavailable(t, "no_voice_runtime", startVoiceSession(t, agentID), "no_voice_runtime")
	})

	t.Run("instance_disabled", func(t *testing.T) {
		instanceID := insertOnlineVoiceInstanceFixture(t, "Gate Disabled Instance", `{"disabled":true}`)
		agentID := insertVoiceBoundAgentFixture(t, "Gate Disabled Agent", instanceID)
		expectVoiceUnavailable(t, "instance_disabled", startVoiceSession(t, agentID), "instance_disabled")
	})

	t.Run("instance_not_active", func(t *testing.T) {
		instanceID := insertManualVoiceInstanceFixture(t, context.Background(), "Gate Offline Instance", "")
		agentID := insertVoiceBoundAgentFixture(t, "Gate Offline Agent", instanceID)
		expectVoiceUnavailable(t, "instance_not_active", startVoiceSession(t, agentID), "instance_not_active")
	})

	t.Run("capability_mismatch", func(t *testing.T) {
		// Binding gates make this shape unreachable through the API, but the
		// initiation recheck is the last line of defence: a text-family
		// runtime in the voice slot is refused here too.
		agentID := insertVoiceBoundAgentFixture(t, "Gate Mismatch Agent", handlerTestRuntimeID(t))
		expectVoiceUnavailable(t, "capability_mismatch", startVoiceSession(t, agentID), "capability_mismatch")
	})

	t.Run("credential_missing", func(t *testing.T) {
		instanceID := insertOnlineVoiceInstanceFixture(t, "Gate NoKey Instance", "")
		agentID := insertVoiceBoundAgentFixture(t, "Gate NoKey Agent", instanceID)
		expectVoiceUnavailable(t, "credential_missing", startVoiceSession(t, agentID), "credential_missing")
	})

	t.Run("credential_invalid_probe", func(t *testing.T) {
		// The §4.5 probe recorded the credential invalid — a new session must
		// not even try the provider hop.
		stubProbeTarget(t, http.StatusUnauthorized)
		instanceID := insertOnlineVoiceInstanceFixture(t, "Gate BadKey Instance", "")
		agentID := insertVoiceBoundAgentFixture(t, "Gate BadKey Agent", instanceID)
		if w := putRuntimeCredential(t, instanceID, "api_key", voiceTestAPIKey); w.Code != http.StatusOK {
			t.Fatalf("seed credential: %d %s", w.Code, w.Body.String())
		}
		expectVoiceUnavailable(t, "credential_invalid", startVoiceSession(t, agentID), "credential_invalid")
	})

	t.Run("credential_invalid_ciphertext", func(t *testing.T) {
		// The ref points at a row whose ciphertext no box can open (e.g. the
		// deployment secret rotated underneath it).
		stubProbeTarget(t, http.StatusOK)
		instanceID := insertOnlineVoiceInstanceFixture(t, "Gate Corrupt Instance", "")
		agentID := insertVoiceBoundAgentFixture(t, "Gate Corrupt Agent", instanceID)
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted)
			VALUES ($1, 'api_key', '\xdeadbeef'::bytea)
		`, instanceID); err != nil {
			t.Fatalf("seed corrupt credential: %v", err)
		}
		if _, err := testPool.Exec(context.Background(), `
			UPDATE agent_runtime SET credential_ref = $1 WHERE id = $2
		`, instanceID+":api_key", instanceID); err != nil {
			t.Fatalf("point credential_ref: %v", err)
		}
		expectVoiceUnavailable(t, "credential_invalid", startVoiceSession(t, agentID), "credential_invalid")
	})
}

// voiceGatewayServer mounts the initiation route on a real HTTP server so the
// websocket upgrade can hijack the connection (recorder-based tests cannot).
func voiceGatewayServer(t *testing.T) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/api/agents/{id}/voice-session", testHandler.StartVoiceSession)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// dialVoiceSession connects a websocket client to the gateway with cookie
// identity and the workspace header.
func dialVoiceSession(t *testing.T, srv *httptest.Server, agentID string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Cookie", voiceAuthCookie(t, testUserID).String())
	header.Set("X-Workspace-ID", testWorkspaceID)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agents/" + agentID + "/voice-session"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		status := "none"
		if resp != nil {
			status = resp.Status
		}
		t.Fatalf("dial gateway: %v (http %s)", err, status)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// waitFor polls until cond passes or the deadline lapses.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", d)
	}
}

// TestVoiceSessionGate_GatePassCreatesLiveSession is the negative space of
// the gate: with every rule-3 fact healthy, the request gets past the gate
// (no 409), a live_session row is created, and the handler proceeds to the
// upgrade step — which a plain GET (no upgrade headers) fails with a 400.
func TestVoiceSessionGate_GatePassCreatesLiveSession(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "Gate Pass", "")

	w := startVoiceSession(t, agentID)
	if w.Code == http.StatusConflict {
		t.Fatalf("gate refused a healthy setup: %s", w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected the upgrade step's 400 for a non-websocket GET, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM live_session WHERE agent_id = $1`, agentID,
	).Scan(&count); err != nil {
		t.Fatalf("count live sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("live_session rows = %d, want 1", count)
	}
}

// TestVoiceGateway_RelaySetupTranscriptHandle is the §3.5 end-to-end: setup
// injection (instructions + transcription taps + resumption + compression +
// VAD + advanced passthrough), bidirectional relay, transcript write-back,
// resumption-handle persistence, the single terminal transition, and the
// §4.5 negative assertion that the plaintext key appears only in the
// server→provider handshake header.
func TestVoiceGateway_RelaySetupTranscriptHandle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	provider := newStubVoiceProvider(t)
	agentID, _ := seedUsableVoiceSetup(t, "Relay Probe", `{"model":"stubby-live-1","advanced":{"temperature":0.5}}`)
	srv := voiceGatewayServer(t)

	conn := dialVoiceSession(t, srv, agentID)

	// The provider's setupComplete is the first frame the client sees — proof
	// the gateway composed and delivered the server-side setup.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, first, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	var setupComplete map[string]any
	if err := json.Unmarshal(first, &setupComplete); err != nil || setupComplete["setupComplete"] == nil {
		t.Fatalf("expected setupComplete, got %s", first)
	}

	setup := provider.setupsRecorded(t)[0]["setup"].(map[string]any)
	if setup["model"] != "models/stubby-live-1" {
		t.Fatalf("setup model = %v, want the instance's metadata model", setup["model"])
	}
	instr := setup["systemInstruction"].(map[string]any)
	instrText := instr["parts"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(instrText, "VOICE-CONTEXT-MARKER-7q2") {
		t.Fatalf("systemInstruction missing the agent instructions: %q", instrText)
	}
	if modalities := setup["generationConfig"].(map[string]any)["responseModalities"].([]any); len(modalities) != 1 || modalities[0] != "AUDIO" {
		t.Fatalf("responseModalities = %v, want [AUDIO]", modalities)
	}
	for _, key := range []string{"inputAudioTranscription", "outputAudioTranscription", "sessionResumption"} {
		if _, ok := setup[key].(map[string]any); !ok {
			t.Fatalf("setup missing %s", key)
		}
	}
	if _, ok := setup["contextWindowCompression"].(map[string]any)["slidingWindow"]; !ok {
		t.Fatalf("contextWindowCompression missing slidingWindow")
	}
	vad := setup["realtimeInputConfig"].(map[string]any)["automaticActivityDetection"].(map[string]any)
	if vad["startOfSpeechSensitivity"] != "START_SENSITIVITY_LOW" || vad["disabled"] != false {
		t.Fatalf("unexpected VAD block: %v", vad)
	}
	if setup["temperature"] != float64(0.5) {
		t.Fatalf("advanced passthrough missing temperature: %v", setup["temperature"])
	}

	// §4.5: the key rode the handshake header and never the URL.
	if auth, query := provider.handshake(t); auth != voiceTestAPIKey || query != "" {
		t.Fatalf("provider handshake auth=%q query=%q, want the fake key in the header and an empty URL", auth, query)
	}

	// Upstream relay: audio forwarded verbatim.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"realtimeInput":{"audio":{"data":"c3RhZ2Uz","mimeType":"audio/pcm;rate=16000"}}}`)); err != nil {
		t.Fatalf("send upstream frame: %v", err)
	}

	// A client-sent setup frame is swallowed: the server owns setup.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"setup":{"model":"models/client-shadow"}}`)); err != nil {
		t.Fatalf("send shadow setup: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return len(provider.clientMsgs) >= 1
	})
	provider.mu.Lock()
	forwarded := len(provider.clientMsgs)
	setupsSeen := len(provider.setups)
	provider.mu.Unlock()
	if forwarded != 1 {
		t.Fatalf("stub received %d upstream frames, want exactly the 1 audio frame", forwarded)
	}
	if setupsSeen != 1 {
		t.Fatalf("stub received %d setups, want 1 — a client setup frame leaked through", setupsSeen)
	}

	// Downstream: transcript events are relayed AND persisted; the resumption
	// handle lands in the row.
	stubConn := provider.gatewayConn(t)
	provider.sendDownstream(stubConn, map[string]any{
		"serverContent": map[string]any{
			"inputTranscription": map[string]any{"text": "你好小欣"},
		},
	})
	provider.sendDownstream(stubConn, map[string]any{
		"serverContent": map[string]any{
			"outputTranscription": map[string]any{"text": "你好，我在"},
		},
	})
	provider.sendDownstream(stubConn, map[string]any{
		"sessionResumptionUpdate": map[string]any{"newHandle": "stub-handle-42"},
	})
	for _, want := range []string{"你好小欣", "你好，我在", "stub-handle-42"} {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, downstream, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read downstream frame (%s): %v", want, err)
		}
		if !strings.Contains(string(downstream), want) {
			t.Fatalf("downstream frame %s missing %q", downstream, want)
		}
	}

	// Close the client; the gateway must end the session exactly once with
	// the accumulated transcript and handle.
	conn.Close()
	var status, handle, transcriptRaw, snapshotRaw string
	waitFor(t, 5*time.Second, func() bool {
		err := testPool.QueryRow(context.Background(), `
			SELECT status::text, session_handle, transcript::text, context_snapshot::text
			FROM live_session WHERE agent_id = $1
		`, agentID).Scan(&status, &handle, &transcriptRaw, &snapshotRaw)
		return err == nil && status == "ended"
	})

	var transcript []map[string]any
	if err := json.Unmarshal([]byte(transcriptRaw), &transcript); err != nil {
		t.Fatalf("decode transcript: %v (%s)", err, transcriptRaw)
	}
	if len(transcript) != 2 {
		t.Fatalf("transcript = %s, want 2 entries (user + assistant)", transcriptRaw)
	}
	if transcript[0]["role"] != "user" || transcript[0]["text"] != "你好小欣" {
		t.Fatalf("transcript[0] = %v, want the user entry", transcript[0])
	}
	if transcript[1]["role"] != "assistant" || transcript[1]["text"] != "你好，我在" {
		t.Fatalf("transcript[1] = %v, want the assistant entry", transcript[1])
	}
	if handle != "stub-handle-42" {
		t.Fatalf("session_handle = %q, want stub-handle-42", handle)
	}

	var snapshot map[string]any
	if err := json.Unmarshal([]byte(snapshotRaw), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot["model"] != "stubby-live-1" {
		t.Fatalf("snapshot model = %v", snapshot["model"])
	}
	digest := sha256.Sum256([]byte("VOICE-CONTEXT-MARKER-7q2: follow the injected instructions"))
	if snapshot["instructions_sha256"] != hex.EncodeToString(digest[:]) {
		t.Fatalf("snapshot instructions hash = %v", snapshot["instructions_sha256"])
	}

	// §4.5 negative assertion: the plaintext key appears nowhere in the
	// persisted session row, and no recorded upstream frame carried it.
	if strings.Contains(transcriptRaw, voiceTestAPIKey) || strings.Contains(snapshotRaw, voiceTestAPIKey) {
		t.Fatalf("the API key leaked into a persisted live_session column")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for _, msg := range provider.clientMsgs {
		if strings.Contains(string(msg), voiceTestAPIKey) {
			t.Fatalf("the API key leaked into a relayed frame")
		}
	}
}

// TestTextSlotGate_ClosesEveryLegacyBindingBypass pins REVIEW NOTE 1
// (acceptance criterion 2): before stage 3 only the main agent API enforced
// §4.4 rule 2, so a manual gemini_live instance could slip into the text slot
// through the onboarding shim, the agent builder, or the Mika quickstart.
// Every entrypoint must now reject the same instance with the same capability
// error. The builder's runtime switch and rebind share resolveBuilderRuntime
// with the session-create subtest, so one negative path covers all three.
func TestTextSlotGate_ClosesEveryLegacyBindingBypass(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	voiceID := insertManualVoiceInstanceFixture(t, context.Background(), "Text Slot Bypass Probe", "")
	wantMsg := "cannot be used as the text runtime"

	expectCapabilityRejection := func(t *testing.T, entrypoint string, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d: %s", entrypoint, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), wantMsg) {
			t.Fatalf("%s: expected the §4.4 capability rejection, got %s", entrypoint, w.Body.String())
		}
	}

	t.Run("onboarding shim", func(t *testing.T) {
		w := httptest.NewRecorder()
		testHandler.BootstrapOnboardingRuntime(w, newRequest(http.MethodPost, "/api/me/onboarding/runtime-bootstrap", map[string]string{
			"workspace_id": testWorkspaceID,
			"runtime_id":   voiceID,
		}))
		expectCapabilityRejection(t, "onboarding shim", w)
	})

	t.Run("agent builder session", func(t *testing.T) {
		w := httptest.NewRecorder()
		testHandler.CreateAgentBuilderSession(w, newRequest(http.MethodPost, "/api/agent-builder/sessions", map[string]string{
			"runtime_id": voiceID,
		}))
		expectCapabilityRejection(t, "agent builder session", w)
	})

	t.Run("mika quickstart", func(t *testing.T) {
		// resolveMikaAgent hands back any already-provisioned Mika before it
		// ever looks at the runtime, so the rejection is only observable in a
		// workspace without her.
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE workspace_id = $1 AND system_key = $2`,
			testWorkspaceID, service.MikaSystemKey)
		t.Cleanup(func() {
			testPool.Exec(context.Background(),
				`DELETE FROM agent WHERE workspace_id = $1 AND system_key = $2`,
				testWorkspaceID, service.MikaSystemKey)
		})

		w := createMika(t, map[string]string{
			"runtime_id": voiceID,
			"language":   "en",
		})
		expectCapabilityRejection(t, "mika quickstart", w)
	})
}

// dialVoiceSessionFirstFrame connects a websocket client with NO identity
// headers or cookie — the mobile upgrade — and returns the connection for
// the test to drive the auth frame by hand.
func dialVoiceSessionFirstFrame(t *testing.T, srv *httptest.Server, agentID string, query string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/agents/" + agentID + "/voice-session" + query
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial gateway (first-frame path): %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readVoiceFrame reads one text frame with a bounded deadline.
func readVoiceFrame(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read gateway frame: %v", err)
	}
	return data
}

// expectVoiceFrameCode asserts the post-upgrade rejection contract: one JSON
// error frame carrying `code`, then the connection goes away.
func expectVoiceFrameCode(t *testing.T, entrypoint string, conn *websocket.Conn, wantCode string) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(readVoiceFrame(t, conn), &body); err != nil {
		t.Fatalf("%s: decode error frame: %v", entrypoint, err)
	}
	if body["code"] != wantCode {
		t.Fatalf("%s: code = %q, want %q", entrypoint, body["code"], wantCode)
	}
}

// voiceFirstFrameToken mints the first-frame auth payload's token for
// testUserID.
func voiceFirstFrameToken(t *testing.T) string {
	t.Helper()
	return voiceAuthCookie(t, testUserID).Value
}

// TestVoiceSessionFirstFrameAuth covers the RUYI-449 header-less path end to
// end: the mobile upgrade authenticates from the first frame and then meets
// the identical gate chain; failures arrive as error frames, not statuses.
func TestVoiceSessionFirstFrameAuth(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	t.Run("auth+gate pass relays", func(t *testing.T) {
		provider := newStubVoiceProvider(t)
		agentID, _ := seedUsableVoiceSetup(t, "FirstFrame Pass", "")
		srv := voiceGatewayServer(t)

		conn := dialVoiceSessionFirstFrame(t, srv, agentID, "?workspace_id="+testWorkspaceID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"auth","payload":{"token":"`+voiceFirstFrameToken(t)+`"}}`)); err != nil {
			t.Fatalf("send auth frame: %v", err)
		}

		// setupComplete is the first downstream frame — the auth frame was
		// consumed server-side and the relay is live.
		var first map[string]any
		if err := json.Unmarshal(readVoiceFrame(t, conn), &first); err != nil || first["setupComplete"] == nil {
			t.Fatalf("expected setupComplete after first-frame auth, got %s", first)
		}
		if _, query := provider.handshake(t); query != "" {
			t.Fatalf("provider URL query = %q, want empty", query)
		}
	})

	t.Run("malformed first frame", func(t *testing.T) {
		newStubVoiceProvider(t)
		agentID, _ := seedUsableVoiceSetup(t, "FirstFrame Malformed", "")
		srv := voiceGatewayServer(t)

		conn := dialVoiceSessionFirstFrame(t, srv, agentID, "?workspace_id="+testWorkspaceID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello"}`)); err != nil {
			t.Fatalf("send malformed frame: %v", err)
		}
		expectVoiceFrameCode(t, "malformed first frame", conn, "VOICE_AUTH_FAILED")
	})

	t.Run("invalid token", func(t *testing.T) {
		newStubVoiceProvider(t)
		agentID, _ := seedUsableVoiceSetup(t, "FirstFrame BadToken", "")
		srv := voiceGatewayServer(t)

		conn := dialVoiceSessionFirstFrame(t, srv, agentID, "?workspace_id="+testWorkspaceID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"auth","payload":{"token":"not-a-jwt"}}`)); err != nil {
			t.Fatalf("send auth frame: %v", err)
		}
		expectVoiceFrameCode(t, "invalid token", conn, "VOICE_AUTH_FAILED")
	})

	t.Run("gate rejection as frame", func(t *testing.T) {
		newStubVoiceProvider(t)
		// An agent with no voice runtime — the same §4.4 fact the 409 path
		// refuses, now delivered inside the websocket.
		agentID := dbfx.Agent(t, "FirstFrame NoVoice Agent", handlerTestRuntimeID(t))
		srv := voiceGatewayServer(t)

		conn := dialVoiceSessionFirstFrame(t, srv, agentID, "?workspace_id="+testWorkspaceID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"auth","payload":{"token":"`+voiceFirstFrameToken(t)+`"}}`)); err != nil {
			t.Fatalf("send auth frame: %v", err)
		}
		expectVoiceFrameCode(t, "gate rejection", conn, "VOICE_UNAVAILABLE:no_voice_runtime")
	})

	t.Run("workspace required", func(t *testing.T) {
		newStubVoiceProvider(t)
		agentID, _ := seedUsableVoiceSetup(t, "FirstFrame NoWS", "")
		srv := voiceGatewayServer(t)

		conn := dialVoiceSessionFirstFrame(t, srv, agentID, "")
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"auth","payload":{"token":"`+voiceFirstFrameToken(t)+`"}}`)); err != nil {
			t.Fatalf("send auth frame: %v", err)
		}
		expectVoiceFrameCode(t, "workspace required", conn, "VOICE_SESSION_REJECTED")
	})
}

// TestVoiceSessionHeaderPathKeeps409 pins the RUYI-425 contract that survived
// the RUYI-449 rework: cookie-authenticated gate rejections are plain 409s
// before any upgrade.
func TestVoiceSessionHeaderPathKeeps409(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := dbfx.Agent(t, "Header Path NoVoice Agent", handlerTestRuntimeID(t))
	expectVoiceUnavailable(t, "header path", startVoiceSession(t, agentID), "no_voice_runtime")
}
