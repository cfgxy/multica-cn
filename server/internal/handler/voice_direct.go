package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/pkg/agentcontext"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Direct-connect voice sessions (RUYI-626 stage 1).
//
// The client dials the provider itself (the RUYI-603 verdict: the server's
// egress to Google is exactly what the deployment cannot afford), so the
// server's role shrinks to what only it can do:
//
//   - POST /api/agents/{id}/voice-direct-session — the §4.4 rule-3 gate chain
//     (shared with the relay gateway via openVoiceSession), the live_session
//     row, and one provider hand-off payload: the decrypted key, the model,
//     and the setup inputs the client now composes the setup frame from.
//   - POST /api/voice-sessions/{id}/complete — the terminal write-back the
//     gateway used to own: the client-collected transcript and resumption
//     handle land in live_session, then flow into the facts/summary
//     projection unchanged.
//   - POST /api/runtimes/{id}/credential-probe — the connectivity probe moved
//     client-side (the server-side probe's verdict reflects the server's
//     network, not the user's); the client reports its outcome here and it
//     feeds the same credential_probe metadata badge.
//
// The relay gateway (voice_gateway.go) stays intact as the fallback path and
// for desktop/web until stage 2 retires it; these endpoints never dial the
// provider and never hold an open websocket.
// ---------------------------------------------------------------------------

// maxDirectTranscriptBytes bounds the complete request body. A live
// conversation's transcription text is a few KB per minute; the ceiling is
// abuse protection, not a working limit.
const maxDirectTranscriptBytes = 1 << 20

// maxDirectTranscriptEntries bounds the transcript row count for the same
// reason; each entry is independently size-bounded by the body cap.
const maxDirectTranscriptEntries = 5000

// voiceDirectSessionPayload is the one response that ever carries the
// plaintext credential to a client. The architecture decision (RUYI-603,
// Owner 2026-10-10) accepts the key on the user's own device in exchange for
// the session actually working; the key still never appears in a URL, a log
// line, or any other endpoint's response.
type voiceDirectSessionPayload struct {
	SessionID     string                     `json:"session_id"`
	ProviderWSURL string                     `json:"provider_ws_url"`
	APIKey        string                     `json:"api_key"`
	Model         string                     `json:"model"`
	Instructions  string                     `json:"instructions"`
	Advanced      map[string]json.RawMessage `json:"advanced"`
}

// StartVoiceDirectSession handles POST /api/agents/{id}/voice-direct-session:
// the same §4.4 gate chain as the relay start, then instead of a websocket
// upgrade the handler returns everything the client needs to dial the
// provider and speak the setup frame itself.
func (h *Handler) StartVoiceDirectSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	agentUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "agent_id")
	if !ok {
		return
	}
	plan, hsErr := h.openVoiceSession(r.Context(), userID, h.resolveWorkspaceID(r), agentUUID)
	if hsErr != nil {
		hsErr.writeHTTP(w)
		return
	}
	writeJSON(w, http.StatusOK, voiceDirectSessionPayload{
		SessionID:     uuidToString(plan.session.ID),
		ProviderWSURL: h.voiceProviderDirectURL(),
		APIKey:        plan.apiKey,
		Model:         plan.model,
		Instructions:  plan.instructions,
		Advanced:      plan.advanced,
	})
}

// voiceProviderDirectURL resolves the provider endpoint handed to direct
// clients. Tests point it at an in-process stub via the same injectable the
// gateway dials (VoiceProviderWSBaseURL); production default is the public
// Gemini endpoint.
func (h *Handler) voiceProviderDirectURL() string {
	return h.voiceProviderWSBaseURL() + voiceProviderWSPath
}

// completeVoiceDirectRequest is the client-collected session record. The
// shape mirrors what the relay gateway persisted on teardown: transcript
// rows, the freshest resumption handle, and the tool frames (empty today —
// the setup frame configures no tools — but the field keeps the wire
// contract aligned with the gateway's write-back input).
type completeVoiceDirectRequest struct {
	Transcript    []voiceTranscriptEntry `json:"transcript"`
	SessionHandle string                 `json:"session_handle"`
	ToolFrames    []struct {
		Name     string          `json:"name"`
		CallID   string          `json:"call_id"`
		Args     json.RawMessage `json:"args"`
		IsResult bool            `json:"is_result"`
	} `json:"tool_frames"`
}

// CompleteVoiceDirectSession handles POST
// /api/voice-sessions/{id}/complete: the direct session's terminal
// transition. Idempotent for client retries (a replayed complete neither
// rewrites the terminal row nor re-runs the write-back), scoped to the
// session's own user, and bounded against oversized bodies.
func (h *Handler) CompleteVoiceDirectSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "sessionId"), "session_id")
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDirectTranscriptBytes)
	var req completeVoiceDirectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorCode(w, http.StatusRequestEntityTooLarge, "VOICE_COMPLETE_INVALID", "transcript payload too large or malformed")
		return
	}
	if len(req.Transcript) > maxDirectTranscriptEntries {
		writeErrorCode(w, http.StatusRequestEntityTooLarge, "VOICE_COMPLETE_INVALID", "too many transcript entries")
		return
	}
	for _, entry := range req.Transcript {
		if entry.Role != "user" && entry.Role != "assistant" {
			writeError(w, http.StatusBadRequest, "transcript roles must be user or assistant")
			return
		}
	}

	ctx := r.Context()
	session, err := h.Queries.GetLiveSession(ctx, sessionID)
	if err != nil || uuidToString(session.UserID) != userID {
		// Not found also for a foreign session: existence is not the
		// caller's business.
		writeError(w, http.StatusNotFound, "live session not found")
		return
	}
	if session.Status != "active" {
		// Idempotent replay: the terminal row and its write-back already
		// landed on the first complete.
		writeJSON(w, http.StatusOK, map[string]any{"session_id": uuidToString(session.ID), "status": session.Status})
		return
	}

	var toolFrames []agentcontext.VoiceToolFrame
	for _, f := range req.ToolFrames {
		toolFrames = append(toolFrames, agentcontext.VoiceToolFrame{Name: f.Name, CallID: f.CallID, Args: f.Args, IsResult: f.IsResult})
	}
	h.endDirectLiveSession(ctx, session, req.Transcript, req.SessionHandle, toolFrames)
	writeJSON(w, http.StatusOK, map[string]any{"session_id": uuidToString(session.ID), "status": "ended"})
}

// endDirectLiveSession is the terminal transition + write-back, shared shape
// with the gateway's teardown: transcript (defaulted), handle, then the
// facts/summary projection. Detached from the request context — the client
// has already hung up by the time this runs, and a cancelled request must
// not drop the write-back.
func (h *Handler) endDirectLiveSession(ctx context.Context, session db.LiveSession, transcript []voiceTranscriptEntry, sessionHandle string, toolFrames []agentcontext.VoiceToolFrame) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if transcript == nil {
		transcript = []voiceTranscriptEntry{}
	}
	encoded, err := json.Marshal(transcript)
	if err != nil {
		encoded = []byte("[]")
	}
	if sessionHandle != "" {
		if err := h.Queries.SetLiveSessionHandle(ctx, db.SetLiveSessionHandleParams{
			ID:            session.ID,
			SessionHandle: sessionHandle,
		}); err != nil {
			slog.Warn("direct session handle write failed", "session_id", uuidToString(session.ID), "error", err)
		}
	}
	// EndLiveSession runs after SetLiveSessionHandle, so its RETURNING row
	// already carries the freshest resumption handle for the write-back.
	ended, err := h.Queries.EndLiveSession(ctx, db.EndLiveSessionParams{
		ID:         session.ID,
		Transcript: encoded,
	})
	if err != nil {
		slog.Error("end direct live session failed", "session_id", uuidToString(session.ID), "error", err)
		return
	}
	h.writeBackVoiceSession(ctx, ended, transcript, toolFrames, true)
}

// reportCredentialProbeRequest is a client-side connectivity outcome. Status
// extends the server probe's enum with "unreachable" (RUYI-619's boundary:
// a network verdict is not a credential verdict, so it never gates a start).
type reportCredentialProbeRequest struct {
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// ReportCredentialProbe handles POST /api/runtimes/{id}/credential-probe:
// the client ran the connectivity check from the network the user actually
// uses and reports the outcome into the same credential_probe metadata badge
// the server-side probe writes. Editors only — the badge is instance state.
func (h *Handler) ReportCredentialProbe(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	rt, err := h.getAgentRuntime(r.Context(), obsmetrics.RuntimeLookupSourceRuntimeAPI, runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found")
	if !ok {
		return
	}
	if !canEditRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "you can only edit your own runtimes")
		return
	}

	var req reportCredentialProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch req.Status {
	case "ok", "invalid", "unreachable":
	default:
		writeError(w, http.StatusBadRequest, "status must be ok, invalid, or unreachable")
		return
	}

	probe := CredentialProbeResult{
		Status:     req.Status,
		HTTPStatus: req.HTTPStatus,
		CheckedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	h.recordCredentialProbeOutcome(r.Context(), runtimeUUID, rt.Metadata, probe)
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime_id": runtimeID,
		"probe":      probe,
	})
}
