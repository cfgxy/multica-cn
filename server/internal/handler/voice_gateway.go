package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Voice gateway (RUYI-425 stage 3, design §3.5/§4.4/§4.5)
//
// The gateway is a relaying BidiGenerateContent peer: it owns the provider
// credential (the plaintext API key never leaves the server→provider hop),
// composes the setup frame server-side (Agent instructions + transcription
// write-back configs + resumption + compression), persists the transcript and
// resumption handle into live_session, and enforces the §4.4 rule-3
// initiation recheck before any websocket upgrade.
// ---------------------------------------------------------------------------

const (
	// voiceProviderWSPath is the v1beta BidiGenerateContent websocket route
	// (demo LiveProtocol.kt; identical constant the official SDK dials).
	voiceProviderWSPath = "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	// defaultVoiceProviderWSBaseURL is overridable so tests point the gateway
	// at an in-process stub provider and self-hosted deployments can front a
	// proxy. Env: MULTICA_GEMINI_WS_BASE_URL (router wiring).
	DefaultVoiceProviderWSBaseURL = "wss://generativelanguage.googleapis.com"
	// defaultVoiceModel is the §4.3 metadata.model fallback (demo's current
	// default); an instance's stored model always wins.
	defaultVoiceModel = "gemini-3.8-live"
)

// voiceUpgrader accepts the desktop client's upgrade. Origin is not a security
// boundary here — every mutation on the session is already behind workspace
// membership and the §4.4 gate, and the Electron shell presents a file:// or
// app:// origin that no allowlist could name.
var voiceUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// reservedSetupKeys are the setup fields the server composes; an instance's
// §4.3 advanced JSON may add provider options around them but never override
// them (the model/instructions/transcription pipeline is the gateway's
// contract with the write-back layer).
var reservedSetupKeys = map[string]bool{
	"model":                    true,
	"systemInstruction":        true,
	"generationConfig":         true,
	"inputAudioTranscription":  true,
	"outputAudioTranscription": true,
	"realtimeInputConfig":      true,
	"sessionResumption":        true,
	"contextWindowCompression": true,
}

type voiceGateRejection struct {
	reason string
	detail string
}

// voiceUnavailable writes the §4.4 rule-3 rejection: 409 with a stable
// VOICE_UNAVAILABLE:<reason> code so clients can render the §4.5 degraded
// entry states (hidden / greyed / error) without parsing English sentences.
func voiceUnavailable(w http.ResponseWriter, reason, detail string) {
	writeErrorCode(w, http.StatusConflict, "VOICE_UNAVAILABLE:"+reason, detail)
}

// runtimeMetadataBag decodes an instance's metadata JSONB; a malformed bag
// degrades to empty (metadata is advisory except for the flags below, which
// treat undecodable as unset).
func runtimeMetadataBag(metadata []byte) map[string]json.RawMessage {
	var bag map[string]json.RawMessage
	if len(metadata) > 0 && json.Unmarshal(metadata, &bag) == nil {
		return bag
	}
	return map[string]json.RawMessage{}
}

// runtimeMetadataFlag reads a boolean metadata key.
func runtimeMetadataFlag(rt db.AgentRuntime, key string) bool {
	var flag bool
	if raw, ok := runtimeMetadataBag(rt.Metadata)[key]; ok && json.Unmarshal(raw, &flag) == nil {
		return flag
	}
	return false
}

// runtimeMetadataString reads a string metadata key, "" when absent.
func runtimeMetadataString(rt db.AgentRuntime, key string) string {
	var value string
	if raw, ok := runtimeMetadataBag(rt.Metadata)[key]; ok && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

// voiceCredentialKey extracts the secret-store key half of the instance's
// credential_ref ("<instance-uuid>:<key>").
func voiceCredentialKey(rt db.AgentRuntime) (string, bool) {
	if !rt.CredentialRef.Valid {
		return "", false
	}
	ref := rt.CredentialRef.String
	idx := strings.LastIndex(ref, ":")
	if idx < 0 || idx == len(ref)-1 {
		return "", false
	}
	return ref[idx+1:], true
}

// lastVoiceProbeInvalid reports whether the §4.5 connectivity probe last
// recorded the credential as invalid — the "失效" signal of §4.4 rule 3. A
// missing or skipped probe never blocks: Gemini re-authenticates at dial time
// and the relay surfaces a provider rejection then.
func lastVoiceProbeInvalid(rt db.AgentRuntime) bool {
	var probe struct {
		Status string `json:"status"`
	}
	if raw, ok := runtimeMetadataBag(rt.Metadata)[credentialProbeMetadataKey]; ok && json.Unmarshal(raw, &probe) == nil {
		return probe.Status == "invalid"
	}
	return false
}

// resolveVoiceSessionTarget is the §4.4 rule-3 initiation recheck: binding
// gates validated these same facts when the agent was saved, but a voice
// instance's health is a living property (disabled flag, offline status,
// rotated/invalidated credential), so every session start re-derives it.
// Rejection order puts operator intent (disabled) ahead of transient state
// (status) and static Type-layer facts (capability) ahead of credentials.
// The returned credential is the decrypted API key for the provider hop only.
func (h *Handler) resolveVoiceSessionTarget(ctx context.Context, agentRow db.Agent) (db.AgentRuntime, string, *voiceGateRejection, error) {
	if !agentRow.VoiceRuntimeID.Valid {
		return db.AgentRuntime{}, "", &voiceGateRejection{"no_voice_runtime", "this agent has no voice runtime bound"}, nil
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, agentRow.VoiceRuntimeID)
	if err != nil || rt.WorkspaceID != agentRow.WorkspaceID {
		return db.AgentRuntime{}, "", &voiceGateRejection{"no_voice_runtime", "the bound voice runtime no longer exists"}, nil
	}
	if runtimeMetadataFlag(rt, "disabled") {
		return db.AgentRuntime{}, "", &voiceGateRejection{"instance_disabled", "this voice instance is disabled"}, nil
	}
	if rt.Status != "online" {
		return db.AgentRuntime{}, "", &voiceGateRejection{"instance_not_active", "this voice instance is not active"}, nil
	}
	caps, err := h.runtimeInstanceCapabilities(ctx, rt)
	if err != nil {
		return db.AgentRuntime{}, "", nil, fmt.Errorf("resolve voice runtime capabilities: %w", err)
	}
	if err := validateVoiceCapability(rt, caps); err != nil {
		return db.AgentRuntime{}, "", &voiceGateRejection{"capability_mismatch", err.Error()}, nil
	}

	keyName, ok := voiceCredentialKey(rt)
	if !ok {
		return db.AgentRuntime{}, "", &voiceGateRejection{"credential_missing", "this voice instance has no API key configured"}, nil
	}
	credential, err := h.Queries.GetRuntimeCredential(ctx, db.GetRuntimeCredentialParams{
		RuntimeInstanceID: rt.ID,
		CredentialKey:     keyName,
	})
	if err != nil {
		return db.AgentRuntime{}, "", &voiceGateRejection{"credential_missing", "this voice instance's API key is missing"}, nil
	}
	if lastVoiceProbeInvalid(rt) {
		return db.AgentRuntime{}, "", &voiceGateRejection{"credential_invalid", "this voice instance's API key failed its last connectivity check"}, nil
	}
	if h.RuntimeCredentialBox == nil {
		// Same fail-closed contract as the credential PUT: without the
		// encryption key the server must not fall back to plaintext hops.
		return db.AgentRuntime{}, "", nil, fmt.Errorf("runtime credential encryption is not configured")
	}
	plaintext, err := h.RuntimeCredentialBox.Open(credential.SecretEncrypted)
	if err != nil {
		return db.AgentRuntime{}, "", &voiceGateRejection{"credential_invalid", "this voice instance's stored API key cannot be decrypted"}, nil
	}
	return rt, string(plaintext), nil, nil
}

// validateVoiceCapability is the rule-3 mirror check of the binding gate.
func validateVoiceCapability(rt db.AgentRuntime, caps agent.Capabilities) error {
	if caps.Supports(agent.CapabilityRealtimeVoice) {
		return nil
	}
	return fmt.Errorf(
		"runtime protocol family %q cannot be used as the voice runtime: it does not declare the %q capability",
		rt.Provider, agent.CapabilityRealtimeVoice,
	)
}

// voiceTranscriptEntry is one transcript row inside live_session.transcript.
type voiceTranscriptEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
	At   string `json:"at"`
}

// liveSessionSnapshot freezes the §4.5 配置变更版本一致性 contract: the
// parameters this session runs under, hash-pinned, so write-back audits can
// answer "which context version spoke".
func liveSessionSnapshot(agentRow db.Agent, rt db.AgentRuntime, model string) []byte {
	digest := sha256.Sum256([]byte(agentRow.Instructions))
	snapshot, _ := json.Marshal(map[string]any{
		"model":               model,
		"instructions_sha256": hex.EncodeToString(digest[:]),
		"voice_runtime_id":    uuidToString(rt.ID),
		"protocol_family":     rt.Provider,
	})
	return snapshot
}

// composeVoiceSetupFrame builds the BidiGenerateContent setup the gateway
// sends the provider: demo LiveProtocol.kt's shape (RUYI-402's VAD block
// lives under realtimeInputConfig — setup-root placement is rejected by the
// service) plus the three capability switches the demo lacked (§2.5):
// sessionResumption, contextWindowCompression, and both transcription taps
// that feed the write-back.
func composeVoiceSetupFrame(instructions, model string, advanced map[string]json.RawMessage) map[string]any {
	setup := map[string]any{
		"model":                    "models/" + model,
		"generationConfig":         map[string]any{"responseModalities": []string{"AUDIO"}},
		"inputAudioTranscription":  map[string]any{},
		"outputAudioTranscription": map[string]any{},
		"sessionResumption":        map[string]any{},
		"contextWindowCompression": map[string]any{"slidingWindow": map[string]any{}},
		"realtimeInputConfig": map[string]any{
			"automaticActivityDetection": map[string]any{
				"disabled":                 false,
				"startOfSpeechSensitivity": "START_SENSITIVITY_LOW",
			},
		},
	}
	if strings.TrimSpace(instructions) != "" {
		setup["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": instructions}},
		}
	}
	for key, value := range advanced {
		if reservedSetupKeys[key] {
			continue
		}
		setup[key] = value
	}
	return map[string]any{"setup": setup}
}

// isClientSetupFrame reports whether a client frame is a BidiGenerateContent
// setup. The gateway swallows it: setup is composed server-side (instructions,
// transcription taps, credential), and a client-sent one must never reach the
// provider where it could shadow the injected context.
func isClientSetupFrame(data []byte) bool {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(data, &frame); err != nil {
		return false
	}
	_, ok := frame["setup"]
	return ok
}

// liveServerFrame is the subset of provider→gateway frames the write-back
// layer consumes; everything else relays untouched.
type liveServerFrame struct {
	SessionResumptionUpdate *struct {
		NewHandle string `json:"newHandle"`
	} `json:"sessionResumptionUpdate"`
	ServerContent *struct {
		InputTranscription *struct {
			Text string `json:"text"`
		} `json:"inputTranscription"`
		OutputTranscription *struct {
			Text string `json:"text"`
		} `json:"outputTranscription"`
	} `json:"serverContent"`
}

// StartVoiceSession handles GET /api/agents/{agentId}/voice-session: the
// §4.4 rule-3 gate, then a websocket upgrade into the §3.5 relay
// (client ⇄ gateway ⇄ provider).
func (h *Handler) StartVoiceSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	agentUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "agent_id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
	if !ok {
		return
	}
	agentRow, err := h.Queries.GetAgent(r.Context(), agentUUID)
	if err != nil || uuidToString(agentRow.WorkspaceID) != workspaceID {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	rt, apiKey, rejection, serverErr := h.resolveVoiceSessionTarget(r.Context(), agentRow)
	if serverErr != nil {
		slog.Error("voice session gate failed", "agent_id", agentUUID, "error", serverErr)
		writeError(w, http.StatusServiceUnavailable, "voice session unavailable due to a server configuration problem")
		return
	}
	if rejection != nil {
		voiceUnavailable(w, rejection.reason, rejection.detail)
		return
	}
	if !canUseRuntimeForAgent(member, rt) {
		writeError(w, http.StatusForbidden, "you cannot use this agent's voice runtime")
		return
	}

	model := runtimeMetadataString(rt, "model")
	if model == "" {
		model = defaultVoiceModel
	}
	var advanced map[string]json.RawMessage
	if raw, ok := runtimeMetadataBag(rt.Metadata)["advanced"]; ok {
		json.Unmarshal(raw, &advanced)
	}

	session, err := h.Queries.CreateLiveSession(r.Context(), db.CreateLiveSessionParams{
		WorkspaceID:       agentRow.WorkspaceID,
		AgentID:           agentRow.ID,
		RuntimeInstanceID: rt.ID,
		UserID:            parseUUID(userID),
		Model:             model,
		ContextSnapshot:   liveSessionSnapshot(agentRow, rt, model),
	})
	if err != nil {
		slog.Error("create live session failed", "agent_id", agentUUID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start voice session")
		return
	}

	h.relayVoiceSession(w, r, session, agentRow.Instructions, model, advanced, apiKey)
}

// relayVoiceSession pumps frames both ways until either side disconnects,
// then ends the live session exactly once — the terminal write carries the
// full transcript so an abnormal disconnect still persists everything the
// write-back received (§3.5; the transcript is a projection, the facts layer
// lands in stage 4).
func (h *Handler) relayVoiceSession(
	w http.ResponseWriter,
	r *http.Request,
	session db.LiveSession,
	instructions, model string,
	advanced map[string]json.RawMessage,
	apiKey string,
) {
	// Dial the provider before upgrading the client so connect failures
	// surface as HTTP status codes instead of a websocket that dies mid-handshake.
	dialCtx, cancelDial := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancelDial()
	// The key rides the x-goog-api-key header only — never the URL, never a
	// frame (§4.5; negative-asserted in tests).
	providerConn, _, err := websocket.DefaultDialer.DialContext(dialCtx,
		h.voiceProviderWSBaseURL()+voiceProviderWSPath,
		http.Header{"x-goog-api-key": []string{apiKey}},
	)
	if err != nil {
		slog.Warn("voice provider dial failed", "session_id", uuidToString(session.ID), "error", err)
		writeErrorCode(w, http.StatusBadGateway, "VOICE_PROVIDER_UNREACHABLE", "could not reach the voice provider")
		h.endLiveSession(context.Background(), session, nil)
		return
	}
	defer providerConn.Close()

	if err := providerConn.WriteJSON(composeVoiceSetupFrame(instructions, model, advanced)); err != nil {
		slog.Warn("voice setup write failed", "session_id", uuidToString(session.ID), "error", err)
		h.endLiveSession(context.Background(), session, nil)
		return
	}

	clientConn, err := voiceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error.
		h.endLiveSession(context.Background(), session, nil)
		return
	}
	defer clientConn.Close()

	var transcript []voiceTranscriptEntry
	// Exactly one terminal write covers every teardown path from here: client
	// disconnect, provider close, or a relay write failure. The closure reads
	// the slice variable at defer-run time, so EndLiveSession carries
	// whatever the pump accumulated.
	defer func() { h.endLiveSession(context.Background(), session, transcript) }()

	// The transcript is owned by the provider pump (single writer); the
	// client pump only closes the provider conn to tear both down.
	flushTranscript := func() {
		encoded, err := json.Marshal(transcript)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Queries.SetLiveSessionTranscript(ctx, db.SetLiveSessionTranscriptParams{
			ID:         session.ID,
			Transcript: encoded,
		}); err != nil {
			slog.Warn("live session transcript flush failed", "session_id", uuidToString(session.ID), "error", err)
		}
	}

	// client → provider. Setup frames are swallowed (server composed the
	// setup; a client one must not shadow the injected instructions).
	go func() {
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				providerConn.Close()
				return
			}
			if isClientSetupFrame(data) {
				continue
			}
			if err := providerConn.WriteMessage(msgType, data); err != nil {
				clientConn.Close()
				return
			}
		}
	}()

	// provider → client, with transcript + resumption-handle write-back.
	for {
		_, data, err := providerConn.ReadMessage()
		if err != nil {
			return
		}
		var frame liveServerFrame
		if json.Unmarshal(data, &frame) == nil {
			if frame.SessionResumptionUpdate != nil && frame.SessionResumptionUpdate.NewHandle != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := h.Queries.SetLiveSessionHandle(ctx, db.SetLiveSessionHandleParams{
					ID:            session.ID,
					SessionHandle: frame.SessionResumptionUpdate.NewHandle,
				})
				cancel()
				if err != nil {
					slog.Warn("live session handle write failed", "session_id", uuidToString(session.ID), "error", err)
				}
			}
			if content := frame.ServerContent; content != nil {
				var entry *voiceTranscriptEntry
				if content.InputTranscription != nil && content.InputTranscription.Text != "" {
					entry = &voiceTranscriptEntry{Role: "user", Text: content.InputTranscription.Text}
				} else if content.OutputTranscription != nil && content.OutputTranscription.Text != "" {
					entry = &voiceTranscriptEntry{Role: "assistant", Text: content.OutputTranscription.Text}
				}
				if entry != nil {
					entry.At = time.Now().UTC().Format(time.RFC3339)
					transcript = append(transcript, *entry)
					flushTranscript()
				}
			}
		}
		if err := clientConn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

// endLiveSession is the single terminal transition; safe to call from every
// teardown path. transcript may be nil (nothing transcribed yet) — the column
// default '[]' shape is preserved.
func (h *Handler) endLiveSession(ctx context.Context, session db.LiveSession, transcript []voiceTranscriptEntry) {
	if transcript == nil {
		transcript = []voiceTranscriptEntry{}
	}
	encoded, err := json.Marshal(transcript)
	if err != nil {
		encoded = []byte("[]")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := h.Queries.EndLiveSession(ctx, db.EndLiveSessionParams{
		ID:         session.ID,
		Transcript: encoded,
	}); err != nil {
		slog.Error("end live session failed", "session_id", uuidToString(session.ID), "error", err)
	}
}

// voiceProviderWSBaseURL resolves the injectable provider endpoint.
func (h *Handler) voiceProviderWSBaseURL() string {
	if h.VoiceProviderWSBaseURL != "" {
		return h.VoiceProviderWSBaseURL
	}
	return DefaultVoiceProviderWSBaseURL
}
