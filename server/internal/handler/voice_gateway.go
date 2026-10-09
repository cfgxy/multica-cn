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
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/agentcontext"
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
	rt, err := h.getAgentRuntime(ctx, obsmetrics.RuntimeLookupSourceVoiceGateway, agentRow.VoiceRuntimeID)
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
	ToolCall *struct {
		FunctionCalls []struct {
			ID   string          `json:"id"`
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"functionCalls"`
	} `json:"toolCall"`
}

// liveClientToolFrame is the client→gateway half: function responses
// returning tool results upstream.
type liveClientToolFrame struct {
	ToolResponse *struct {
		FunctionResponses []struct {
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Response json.RawMessage `json:"response"`
		} `json:"functionResponses"`
	} `json:"toolResponse"`
}

// voiceDisablePollInterval is the effective mid-session disable poll cadence
// (§4.5: 禁用 → gateway 优雅终止). Zero on the struct means the production
// default; tests shrink it so the termination path runs in milliseconds.
func (h *Handler) voiceDisablePollInterval() time.Duration {
	if h.VoiceDisablePollInterval > 0 {
		return h.VoiceDisablePollInterval
	}
	return 3 * time.Second
}

// watchVoiceInstanceDisabled polls the voice instance's disabled flag while
// the relay is in flight. The moment the operator's toggle lands, both sides
// get a normal-close frame (1000) and the pumps unblock — teardown flows
// through the relay's single endLiveSession defer, so the write-back
// completes exactly as on any other disconnect. Poll errors are logged and
// retried next tick: a transient DB blip must not kill a live conversation.
// WriteControl is safe concurrently with the pump writes (gorilla contract).
func watchVoiceInstanceDisabled(
	ctx context.Context,
	h *Handler,
	instanceID pgtype.UUID,
	providerConn, clientConn *websocket.Conn,
	stop <-chan struct{},
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			rt, err := h.getAgentRuntime(ctx, obsmetrics.RuntimeLookupSourceVoiceGateway, instanceID)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("voice disable poll failed", "instance_id", uuidToString(instanceID), "error", err)
				}
				continue
			}
			if !runtimeMetadataFlag(rt, "disabled") {
				continue
			}
			for _, conn := range []*websocket.Conn{clientConn, providerConn} {
				deadline := time.Now().Add(2 * time.Second)
				if err := conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "voice instance disabled"),
					deadline); err != nil {
					slog.Warn("voice disable close frame failed", "instance_id", uuidToString(instanceID), "error", err)
				}
				conn.SetReadDeadline(time.Now())
			}
			return
		}
	}
}

// StartVoiceSession handles GET /api/agents/{agentId}/voice-session: the
// §4.4 rule-3 gate, then a websocket upgrade into the §3.5 relay
// (client ⇄ gateway ⇄ provider).
//
// The route lives outside the Auth middleware group, so identity comes from
// two surfaces (RUYI-449): the session cookie / bearer header for browser
// and desktop clients — rejections stay plain HTTP statuses there — and,
// when neither is present or valid, the first websocket frame for the
// header-less mobile upgrade (the RUYI-429 realtime pattern). Both paths
// run the identical gate chain in openVoiceSession; only the failure
// transport differs.
func (h *Handler) StartVoiceSession(w http.ResponseWriter, r *http.Request) {
	agentUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "agent_id")
	if !ok {
		return
	}
	if userID := h.voiceRequestUserID(r); userID != "" {
		plan, hsErr := h.openVoiceSession(r.Context(), userID, h.resolveVoiceWorkspaceID(r), agentUUID, voiceSessionModeGateway)
		if hsErr != nil {
			hsErr.writeHTTP(w)
			return
		}
		h.relayVoiceSession(w, r, plan.session, plan.instructions, plan.model, plan.advanced, plan.apiKey)
		return
	}
	h.startVoiceSessionFirstFrame(w, r, agentUUID)
}

// voiceSessionPlan carries everything the relay needs once the §4.4 gate
// chain has passed and the live session row exists.
type voiceSessionPlan struct {
	session      db.LiveSession
	apiKey       string
	instructions string
	model        string
	advanced     map[string]json.RawMessage
}

// Transport modes for live_session rows (RUYI-626): 'gateway' rows were
// relayed by the server hop, 'direct' rows were handed off to a client that
// dials the provider itself. The DB CHECK (migration 937) pins the enum;
// pre-direct rows backfilled 'gateway'.
const (
	voiceSessionModeGateway = "gateway"
	voiceSessionModeDirect  = "direct"
)

// voiceHandshakeError is a guard/gate failure both start paths can express:
// an HTTP status pre-upgrade, a JSON error frame post-upgrade.
type voiceHandshakeError struct {
	status int
	code   string // stable machine code; empty means plain-text error only
	detail string
}

func (e *voiceHandshakeError) writeHTTP(w http.ResponseWriter) {
	if e.code != "" {
		writeErrorCode(w, e.status, e.code, e.detail)
		return
	}
	writeError(w, e.status, e.detail)
}

// openVoiceSession runs the full guard chain — workspace membership, agent
// existence, the §4.4 rule-3 initiation recheck, the actor-vs-runtime slot
// check — and creates the live session row tagged with the caller's
// transport mode (RUYI-626). Identity and workspace have already been
// resolved by the caller. The rejection order mirrors the pre-RUYI-449
// header path exactly.
func (h *Handler) openVoiceSession(ctx context.Context, userID, workspaceID string, agentUUID pgtype.UUID, mode string) (*voiceSessionPlan, *voiceHandshakeError) {
	if workspaceID == "" {
		return nil, &voiceHandshakeError{status: http.StatusBadRequest, detail: "workspace_id is required"}
	}
	member, err := h.getWorkspaceMember(ctx, userID, workspaceID)
	if err != nil {
		return nil, &voiceHandshakeError{status: http.StatusNotFound, detail: "workspace not found"}
	}
	agentRow, err := h.Queries.GetAgent(ctx, agentUUID)
	if err != nil || uuidToString(agentRow.WorkspaceID) != workspaceID {
		return nil, &voiceHandshakeError{status: http.StatusNotFound, detail: "agent not found"}
	}

	rt, apiKey, rejection, serverErr := h.resolveVoiceSessionTarget(ctx, agentRow)
	if serverErr != nil {
		slog.Error("voice session gate failed", "agent_id", uuidToString(agentUUID), "error", serverErr)
		return nil, &voiceHandshakeError{status: http.StatusServiceUnavailable, detail: "voice session unavailable due to a server configuration problem"}
	}
	if rejection != nil {
		return nil, &voiceHandshakeError{status: http.StatusConflict, code: "VOICE_UNAVAILABLE:" + rejection.reason, detail: rejection.detail}
	}
	if !canUseRuntimeForAgent(member, rt) {
		return nil, &voiceHandshakeError{status: http.StatusForbidden, detail: "you cannot use this agent's voice runtime"}
	}

	model := runtimeMetadataString(rt, "model")
	if model == "" {
		model = defaultVoiceModel
	}
	var advanced map[string]json.RawMessage
	if raw, ok := runtimeMetadataBag(rt.Metadata)["advanced"]; ok {
		json.Unmarshal(raw, &advanced)
	}

	session, err := h.Queries.CreateLiveSession(ctx, db.CreateLiveSessionParams{
		WorkspaceID:       agentRow.WorkspaceID,
		AgentID:           agentRow.ID,
		RuntimeInstanceID: rt.ID,
		UserID:            parseUUID(userID),
		Model:             model,
		ContextSnapshot:   liveSessionSnapshot(agentRow, rt, model),
		Mode:              mode,
	})
	if err != nil {
		slog.Error("create live session failed", "agent_id", uuidToString(agentUUID), "error", err)
		return nil, &voiceHandshakeError{status: http.StatusInternalServerError, detail: "failed to start voice session"}
	}
	return &voiceSessionPlan{session: session, apiKey: apiKey, instructions: agentRow.Instructions, model: model, advanced: advanced}, nil
}

// voiceRequestUserID resolves the caller identity from the request's bearer
// header or session cookie — the same token surfaces the Auth middleware
// accepts — without the middleware (the voice route is registered outside
// the Auth group). Empty means "no usable header identity", which degrades
// to the first-frame path; a header token that fails validation degrades
// the same way, mirroring realtime.HandleWebSocket's cookie rule.
func (h *Handler) voiceRequestUserID(r *http.Request) string {
	token := voiceRequestToken(r)
	if token == "" {
		return ""
	}
	uid, errMsg := realtime.ResolveUserToken(r.Context(), token, h.VoicePATResolver, h.VoiceDisabledLookup)
	if errMsg != "" {
		slog.Debug("voice: header token rejected; degrading to first-frame auth", "error", errMsg)
		return ""
	}
	return uid
}

// voiceRequestToken extracts the session token from the Authorization
// header or the auth cookie — the same extraction order and prefix rule as
// the middleware's extractToken.
func voiceRequestToken(r *http.Request) string {
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString != authHeader {
			return tokenString
		}
	}
	if cookie, err := r.Cookie(auth.AuthCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return ""
}

// resolveVoiceWorkspaceID resolves the workspace for a voice session start:
// the standard header surfaces first, then the upgrade URL's query string —
// the realtime hub's contract for header-less websocket clients. Empty when
// nothing resolves (membership then fails closed).
func (h *Handler) resolveVoiceWorkspaceID(r *http.Request) string {
	if wsID := h.resolveWorkspaceID(r); wsID != "" {
		return wsID
	}
	if raw := r.URL.Query().Get("workspace_id"); raw != "" {
		if id, err := util.ParseUUID(raw); err == nil {
			return util.UUIDToString(id)
		}
		return ""
	}
	if slug := r.URL.Query().Get("workspace_slug"); slug != "" {
		ws, err := h.Queries.GetWorkspaceBySlug(r.Context(), slug)
		if err != nil {
			return ""
		}
		return util.UUIDToString(ws.ID)
	}
	return ""
}

// voiceAuthReadDeadline matches the realtime hub's first-frame budget.
const voiceAuthReadDeadline = 10 * time.Second

// readVoiceAuthFrame reads the first websocket message expecting the
// realtime auth envelope {"type":"auth","payload":{"token":...}}. A
// non-empty second return is the client-safe rejection text.
func readVoiceAuthFrame(conn *websocket.Conn) (token, errMsg string) {
	conn.SetReadDeadline(time.Now().Add(voiceAuthReadDeadline))
	defer conn.SetReadDeadline(time.Time{})

	_, raw, err := conn.ReadMessage()
	if err != nil {
		return "", "auth timeout or read error"
	}
	var msg struct {
		Type    string `json:"type"`
		Payload struct {
			Token string `json:"token"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "auth" || msg.Payload.Token == "" {
		return "", "expected auth message as first frame"
	}
	return msg.Payload.Token, ""
}

// writeVoiceWSError sends one JSON error frame and tears the connection
// down — the post-upgrade analogue of the HTTP status codes. The frame
// shape mirrors writeErrorCode ({"error", "code"}) so clients parse one
// shape regardless of path.
func writeVoiceWSError(conn *websocket.Conn, code, detail string) {
	payload, err := json.Marshal(map[string]string{"error": detail, "code": code})
	if err != nil {
		payload = []byte(`{"error":"voice session failed","code":"VOICE_SESSION_FAILED"}`)
	}
	if werr := conn.WriteMessage(websocket.TextMessage, payload); werr != nil {
		slog.Warn("voice: failed to send error frame", "code", code, "error", werr)
	}
	conn.Close()
}

// startVoiceSessionFirstFrame serves header-less clients (mobile): upgrade
// immediately, authenticate from the first frame, then run the same gate
// chain. Failures after the upgrade become error frames; the provider dial
// moves after the upgrade too, so its failure surfaces as
// VOICE_PROVIDER_UNREACHABLE instead of HTTP 502.
func (h *Handler) startVoiceSessionFirstFrame(w http.ResponseWriter, r *http.Request, agentUUID pgtype.UUID) {
	conn, err := voiceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error.
		return
	}
	defer conn.Close()

	token, errMsg := readVoiceAuthFrame(conn)
	if errMsg != "" {
		writeVoiceWSError(conn, "VOICE_AUTH_FAILED", errMsg)
		return
	}
	userID, errMsg := realtime.ResolveUserToken(r.Context(), token, h.VoicePATResolver, h.VoiceDisabledLookup)
	if errMsg != "" {
		slog.Debug("voice: first-frame auth rejected", "error", errMsg)
		writeVoiceWSError(conn, "VOICE_AUTH_FAILED", errMsg)
		return
	}

	plan, hsErr := h.openVoiceSession(r.Context(), userID, h.resolveVoiceWorkspaceID(r), agentUUID, voiceSessionModeGateway)
	if hsErr != nil {
		// Guard failures carry no stable code; the frame path always names
		// one so clients branch on `code` uniformly.
		code := hsErr.code
		if code == "" {
			code = "VOICE_SESSION_REJECTED"
		}
		writeVoiceWSError(conn, code, hsErr.detail)
		return
	}

	providerConn, err := dialVoiceProvider(r.Context(), h.voiceProviderWSBaseURL(), plan.apiKey)
	if err != nil {
		slog.Warn("voice provider dial failed", "session_id", uuidToString(plan.session.ID), "error", err)
		writeVoiceWSError(conn, "VOICE_PROVIDER_UNREACHABLE", "could not reach the voice provider")
		h.endLiveSession(context.Background(), plan.session, nil, nil)
		return
	}
	if err := providerConn.WriteJSON(composeVoiceSetupFrame(plan.instructions, plan.model, plan.advanced)); err != nil {
		slog.Warn("voice setup write failed", "session_id", uuidToString(plan.session.ID), "error", err)
		providerConn.Close()
		writeVoiceWSError(conn, "VOICE_PROVIDER_UNREACHABLE", "could not reach the voice provider")
		h.endLiveSession(context.Background(), plan.session, nil, nil)
		return
	}
	h.relayVoiceSessionConn(r, plan.session, providerConn, conn)
}

// dialVoiceProvider opens the provider relay hop. The key rides the
// x-goog-api-key header only — never the URL, never a frame (§4.5;
// negative-asserted in tests).
func dialVoiceProvider(ctx context.Context, baseURL, apiKey string) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	providerConn, _, err := websocket.DefaultDialer.DialContext(dialCtx,
		baseURL+voiceProviderWSPath,
		http.Header{"x-goog-api-key": []string{apiKey}},
	)
	return providerConn, err
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
	providerConn, err := dialVoiceProvider(r.Context(), h.voiceProviderWSBaseURL(), apiKey)
	if err != nil {
		slog.Warn("voice provider dial failed", "session_id", uuidToString(session.ID), "error", err)
		writeErrorCode(w, http.StatusBadGateway, "VOICE_PROVIDER_UNREACHABLE", "could not reach the voice provider")
		h.endLiveSession(context.Background(), session, nil, nil)
		return
	}
	defer providerConn.Close()

	if err := providerConn.WriteJSON(composeVoiceSetupFrame(instructions, model, advanced)); err != nil {
		slog.Warn("voice setup write failed", "session_id", uuidToString(session.ID), "error", err)
		h.endLiveSession(context.Background(), session, nil, nil)
		return
	}

	clientConn, err := voiceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error.
		h.endLiveSession(context.Background(), session, nil, nil)
		return
	}
	h.relayVoiceSessionConn(r, session, providerConn, clientConn)
}

// relayVoiceSessionConn is the pump shared by both start paths; it owns two
// already-live connections (the provider hop and the upgraded client) and
// every teardown flows through the single endLiveSession defer.
func (h *Handler) relayVoiceSessionConn(
	r *http.Request,
	session db.LiveSession,
	providerConn, clientConn *websocket.Conn,
) {
	defer providerConn.Close()
	defer clientConn.Close()

	// §4.5 禁用: a mid-session toggle must gracefully stop the relay. The
	// watcher sends normal-close frames to both sides; teardown then flows
	// through the single endLiveSession defer below — the write-back
	// completes exactly as on any other disconnect. stopWatch is closed by
	// defer before the connection closes run (LIFO), never leaking the
	// goroutine past the handler.
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go watchVoiceInstanceDisabled(r.Context(), h, session.RuntimeInstanceID,
		providerConn, clientConn, stopWatch, h.voiceDisablePollInterval())

	var transcript []voiceTranscriptEntry
	// Tool interactions feed the facts write-back (stage 4); both pumps parse
	// frames, so the slice needs its own lock — the transcript stays
	// single-writer (provider pump only).
	var toolFrames []agentcontext.VoiceToolFrame
	var toolMu sync.Mutex
	recordToolFrame := func(f agentcontext.VoiceToolFrame) {
		toolMu.Lock()
		defer toolMu.Unlock()
		toolFrames = append(toolFrames, f)
	}
	// Exactly one terminal write covers every teardown path from here: client
	// disconnect, provider close, or a relay write failure. The closures read
	// the slice variables at defer-run time, so the terminal write carries
	// whatever the pumps accumulated — an abnormal disconnect loses nothing.
	defer func() {
		toolMu.Lock()
		frames := toolFrames
		toolMu.Unlock()
		h.endLiveSession(context.Background(), session, transcript, frames)
	}()

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
			var toolFrame liveClientToolFrame
			if json.Unmarshal(data, &toolFrame) == nil && toolFrame.ToolResponse != nil {
				for _, resp := range toolFrame.ToolResponse.FunctionResponses {
					recordToolFrame(agentcontext.VoiceToolFrame{Name: resp.Name, CallID: resp.ID, Args: resp.Response, IsResult: true})
				}
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
			if frame.ToolCall != nil {
				for _, call := range frame.ToolCall.FunctionCalls {
					recordToolFrame(agentcontext.VoiceToolFrame{Name: call.Name, CallID: call.ID, Args: call.Args})
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
// default '[]' shape is preserved. After the terminal row lands, the stage-4
// write-back extracts facts and the summary projection: at-least-once with
// database-side idempotency (the (workspace_id, event_id) unique index), so
// retries and replays never duplicate. If the write-back still fails, the
// transcript is already persisted and replayVoiceSessionWriteBack can
// rebuild the projection from it (§3.5 失败语义).
func (h *Handler) endLiveSession(ctx context.Context, session db.LiveSession, transcript []voiceTranscriptEntry, toolFrames []agentcontext.VoiceToolFrame) {
	if transcript == nil {
		transcript = []voiceTranscriptEntry{}
	}
	encoded, err := json.Marshal(transcript)
	if err != nil {
		encoded = []byte("[]")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ended, err := h.Queries.EndLiveSession(ctx, db.EndLiveSessionParams{
		ID:         session.ID,
		Transcript: encoded,
	})
	if err != nil {
		slog.Error("end live session failed", "session_id", uuidToString(session.ID), "error", err)
		return
	}
	h.writeBackVoiceSession(ctx, ended, transcript, toolFrames, true)
}

// writeBackVoiceSession extracts the facts layer and the summary projection
// for a closed session and persists both. Retries bound the transient case;
// the hard-failure case logs and relies on the replay path. SessionHandle
// comes from the session row, so resumption-handle facts survive replays.
func (h *Handler) writeBackVoiceSession(ctx context.Context, session db.LiveSession, transcript []voiceTranscriptEntry, toolFrames []agentcontext.VoiceToolFrame, retry bool) {
	facts := agentcontext.ExtractVoiceFacts(agentcontext.VoiceFactsInput{
		SessionID:     uuidToString(session.ID),
		Entries:       transcriptEntries(transcript),
		ToolFrames:    toolFrames,
		EndedAt:       endedAtOrNow(session),
		SessionHandle: session.SessionHandle,
	})
	summary := agentcontext.BuildVoiceSummary(uuidToString(session.ID), facts, len(agentcontext.MergeVoiceTurns(transcriptEntries(transcript))))

	attempts := 1
	if retry {
		attempts = 3
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		if err = h.insertFactsAndSummary(ctx, session, facts, summary); err == nil {
			return
		}
	}
	// Last resort: the transcript column already holds everything received
	// (EndLiveSession above), so nothing is lost — an operator or a later
	// run can call replayVoiceSessionWriteBack.
	slog.Error("voice write-back failed after retries",
		"session_id", uuidToString(session.ID), "attempts", attempts, "error", err)
}

// insertFactsAndSummary is one idempotent write-back attempt.
func (h *Handler) insertFactsAndSummary(ctx context.Context, session db.LiveSession, facts []agentcontext.Fact, summary string) error {
	for _, f := range facts {
		payload, err := json.Marshal(f.Payload)
		if err != nil {
			payload = []byte("{}")
		}
		recorded, perr := time.Parse(time.RFC3339, f.RecordedAt)
		if perr != nil {
			recorded = time.Now().UTC()
		}
		if _, err := h.Queries.InsertFactEvent(ctx, db.InsertFactEventParams{
			WorkspaceID:   session.WorkspaceID,
			AgentID:       session.AgentID,
			LiveSessionID: session.ID,
			EventID:       f.EventID,
			Seq:           f.Seq,
			SourceRuntime: f.SourceRuntime,
			Kind:          string(f.Kind),
			Payload:       payload,
			EvidenceRef:   f.EvidenceRef,
			RecordedAt:    pgtype.Timestamptz{Time: recorded, Valid: true},
		}); err != nil {
			return err
		}
	}
	return h.Queries.SetLiveSessionSummary(ctx, db.SetLiveSessionSummaryParams{
		ID:      session.ID,
		Summary: summary,
	})
}

// replayVoiceSessionWriteBack rebuilds the projection from the persisted
// authoritative inputs (§3.6-3 重建路径): transcript + session handle →
// facts → summary. v1 boundary: tool interactions live only in the facts
// layer (never in the transcript), so a replay restores decision and
// lifecycle events; tool facts depend on their original insert (the unique
// index keeps either path duplicate-free).
func (h *Handler) replayVoiceSessionWriteBack(ctx context.Context, session db.LiveSession) error {
	var transcript []voiceTranscriptEntry
	if err := json.Unmarshal(session.Transcript, &transcript); err != nil {
		return fmt.Errorf("decode transcript: %w", err)
	}
	h.writeBackVoiceSession(ctx, session, transcript, nil, false)
	return nil
}

// transcriptEntries converts the persisted transcript shape into the
// extraction input.
func transcriptEntries(entries []voiceTranscriptEntry) []agentcontext.VoiceTranscriptEntry {
	out := make([]agentcontext.VoiceTranscriptEntry, len(entries))
	for i, e := range entries {
		out[i] = agentcontext.VoiceTranscriptEntry{Role: e.Role, Text: e.Text, At: e.At}
	}
	return out
}

// endedAtOrNow normalizes the event timestamp: ended sessions carry their
// ended_at, mid-write callers fall back to now.
func endedAtOrNow(session db.LiveSession) time.Time {
	if session.EndedAt.Valid {
		return session.EndedAt.Time
	}
	return time.Now().UTC()
}

// voiceProviderWSBaseURL resolves the injectable provider endpoint.
func (h *Handler) voiceProviderWSBaseURL() string {
	if h.VoiceProviderWSBaseURL != "" {
		return h.VoiceProviderWSBaseURL
	}
	return DefaultVoiceProviderWSBaseURL
}
