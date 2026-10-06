package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Credential connectivity probe (RUYI-425 §4.3/§4.5, stage 2)
//
// Saving or rotating a credential on a voice-family instance triggers a
// lightweight probe against the provider's model-list endpoint: a 2xx means
// the key works, anything else (or an unreachable target) records the
// "invalid" badge state per §4.5. The probe NEVER blocks the save — the
// credential is already committed when the probe runs, and a probe outcome
// that cannot be recorded is a log line, not an error.
//
// The probe target is injectable so tests (and air-gapped deployments) can
// point it at a stub: VoiceProbeBaseURL is wired from
// MULTICA_GEMINI_PROBE_BASE_URL, defaulting to the public Gemini endpoint.
// The credential value travels only in the x-goog-api-key request header —
// never in the URL, never in a log line, never in a stored error detail.
// ---------------------------------------------------------------------------

const DefaultVoiceProbeBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// voiceProbeTimeout bounds the synchronous probe inside the credential PUT:
// saving a key may take at most this much longer than before.
const voiceProbeTimeout = 5 * time.Second

// CredentialProbeResult is the probe outcome returned in the credential PUT
// response and persisted under the instance metadata's credential_probe key.
// It carries no credential material and no provider response body — only the
// coarse status and the HTTP status code.
type CredentialProbeResult struct {
	Status     string `json:"status"` // "ok" | "invalid" | "skipped"
	HTTPStatus int    `json:"http_status,omitempty"`
	CheckedAt  string `json:"checked_at"`
}

// instanceHasVoiceFamily reports whether the instance's Type layer declares
// realtime voice — the only family the probe applies to. Profile lookup
// failures skip the probe (fail-open for the feature; the save is unaffected).
func (h *Handler) instanceHasVoiceFamily(ctx context.Context, rt db.AgentRuntime) bool {
	if !rt.ProfileID.Valid {
		return agent.IsVoiceProtocolFamily(rt.Provider)
	}
	profile, err := h.Queries.GetRuntimeProfile(ctx, rt.ProfileID)
	if err != nil {
		return false
	}
	return agent.IsVoiceProtocolFamily(profile.ProtocolFamily)
}

// probeVoiceCredential checks the just-stored credential value against the
// configured probe target. The returned result is presentation-safe.
func (h *Handler) probeVoiceCredential(ctx context.Context, value string) CredentialProbeResult {
	result := CredentialProbeResult{Status: "skipped", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	baseURL := h.VoiceProbeBaseURL
	if baseURL == "" {
		return result
	}
	client := h.VoiceProbeHTTPClient
	if client == nil {
		client = &http.Client{Timeout: voiceProbeTimeout}
	}

	reqCtx, cancel := context.WithTimeout(ctx, voiceProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		result.Status = "invalid"
		return result
	}
	// The key rides the header Google's API expects — not the query string,
	// so it cannot leak into access logs via the request URL.
	req.Header.Set("x-goog-api-key", value)

	resp, err := client.Do(req)
	if err != nil {
		// Unreachable target counts as a failed probe per §4.5: the badge
		// flips to "invalid" and a fresh save re-probes. The error detail can
		// embed the URL but never the key — it is only logged, never stored.
		slog.Warn("runtime credential probe failed", "error", err)
		result.Status = "invalid"
		return result
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection is reusable, then discard: the
	// body is never parsed, so no provider payload can reach logs or clients.
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	result.HTTPStatus = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Status = "ok"
	} else {
		result.Status = "invalid"
	}
	return result
}

// recordCredentialProbeOutcome merges the probe outcome into the instance's
// metadata bag. Failures are logged and swallowed: the credential is already
// stored, and the badge state self-heals on the next save.
func (h *Handler) recordCredentialProbeOutcome(ctx context.Context, runtimeID pgtype.UUID, metadata []byte, probe CredentialProbeResult) {
	encoded, err := mergeAgentRuntimeMetadataKey(metadata, credentialProbeMetadataKey, probe)
	if err != nil {
		slog.Warn("merge credential probe outcome failed", "runtime_id", uuidToString(runtimeID), "error", err)
		return
	}
	if err := h.Queries.SetAgentRuntimeMetadata(ctx, db.SetAgentRuntimeMetadataParams{
		ID:       runtimeID,
		Metadata: encoded,
	}); err != nil {
		slog.Warn("record credential probe outcome failed", "runtime_id", uuidToString(runtimeID), "error", err)
	}
}

// clearCredentialProbeOutcome removes the probe bag key (credential DELETE):
// a deleted credential has no probe state to show.
func (h *Handler) clearCredentialProbeOutcome(ctx context.Context, runtimeID pgtype.UUID, metadata []byte) {
	encoded, err := mergeAgentRuntimeMetadataKey(metadata, credentialProbeMetadataKey, nil)
	if err != nil {
		slog.Warn("clear credential probe outcome failed", "runtime_id", uuidToString(runtimeID), "error", err)
		return
	}
	if err := h.Queries.SetAgentRuntimeMetadata(ctx, db.SetAgentRuntimeMetadataParams{
		ID:       runtimeID,
		Metadata: encoded,
	}); err != nil {
		slog.Warn("clear credential probe outcome failed", "runtime_id", uuidToString(runtimeID), "error", err)
	}
}

// mergeAgentRuntimeMetadataKey returns the metadata bag with the given key
// set to value (nil removes it), preserving every other key.
func mergeAgentRuntimeMetadataKey(metadata []byte, key string, value any) ([]byte, error) {
	bag := map[string]json.RawMessage{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &bag); err != nil {
			return nil, err
		}
	}
	if value == nil {
		delete(bag, key)
	} else {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		bag[key] = encoded
	}
	return json.Marshal(bag)
}
