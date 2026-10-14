package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Credential probe re-checks (RUYI-692 D1 ③).
//
// The RUYI-626 client-side probe retired with the direct hand-off, so the
// badge's two remaining refresh paths are both server-run:
//
//   - POST /api/runtimes/{id}/credential-probe — the operator (or the entry
//     UI) re-runs the probe on demand. Same path the client reported to; a
//     legacy client body is ignored, never obeyed, so an old client degrades
//     into "one extra server-run probe" instead of writing foreign state.
//   - RefreshVoiceProbeBadges — the timed sweep: degraded badges (invalid /
//     unreachable) re-probe so a healed network or a rotated key on the
//     provider side shows up without operator attention, and a stale ok
//     badge re-verifies itself. Fresh ok badges are left alone: the point is
//     honest state, not load on the provider.
//
// Every probe here answers the same question the session dial asks — can
// THIS server reach the provider (through the configured proxy) with THIS
// key. The four-state semantics (ok/invalid/unreachable/skipped) are
// unchanged from RUYI-425/619.
// ---------------------------------------------------------------------------

// voiceProbeRefreshMaxAge is how long an "ok" badge is trusted before the
// timed sweep re-verifies it. Degraded badges are always re-probed.
const voiceProbeRefreshMaxAge = 30 * time.Minute

// RunCredentialProbe handles POST /api/runtimes/{runtimeId}/credential-probe:
// the server runs the models.list probe now and records the outcome onto the
// credential_probe badge. Editors only — the badge is instance state. The
// request body is not read: under the retired client-report contract this
// endpoint accepted the client's verdict, and obeying a client-declared "ok"
// would let stale state masquerade as a fresh measurement.
func (h *Handler) RunCredentialProbe(w http.ResponseWriter, r *http.Request) {
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

	probe, err := h.runVoiceCredentialProbe(r.Context(), rt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime_id": runtimeID,
		"probe":      probe,
	})
}

// runVoiceCredentialProbe executes one server-side probe against the
// instance's stored credential and records the outcome onto the badge. The
// error return covers "nothing to probe" (no credential ref / missing secret
// row / undecryptable) — operator-actionable state, surfaced as 400.
func (h *Handler) runVoiceCredentialProbe(ctx context.Context, rt db.AgentRuntime) (CredentialProbeResult, error) {
	skip := CredentialProbeResult{Status: credentialProbeSkipped, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if !h.instanceHasVoiceFamily(ctx, rt) {
		return skip, nil
	}
	keyName, ok := voiceCredentialKey(rt)
	if !ok {
		return skip, errVoiceProbeNoCredential
	}
	credential, err := h.Queries.GetRuntimeCredential(ctx, db.GetRuntimeCredentialParams{
		RuntimeInstanceID: rt.ID,
		CredentialKey:     keyName,
	})
	if err != nil {
		return skip, errVoiceProbeNoCredential
	}
	if h.RuntimeCredentialBox == nil {
		return skip, errVoiceProbeEncryptionMissing
	}
	plaintext, err := h.RuntimeCredentialBox.Open(credential.SecretEncrypted)
	if err != nil {
		return skip, errVoiceProbeUndecryptable
	}

	probe := h.probeVoiceCredential(ctx, string(plaintext))
	if probe.Status != credentialProbeSkipped {
		h.recordCredentialProbeOutcome(ctx, rt.ID, rt.Metadata, probe)
	}
	slog.Info("runtime credential re-probed", "runtime_id", uuidToString(rt.ID), "probe_status", probe.Status, "http_status", probe.HTTPStatus)
	return probe, nil
}

// errVoiceProbe* separate "why nothing was probed" so the endpoint can say
// something actionable; the strings are client-safe (no credential material
// by construction).
var (
	errVoiceProbeNoCredential      = errorString("this voice instance has no API key configured")
	errVoiceProbeEncryptionMissing = errorString("runtime credential encryption is not configured on this server")
	errVoiceProbeUndecryptable     = errorString("the stored credential could not be decrypted; save it again")
)

// errorString is a tiny constant-error type keeping the probe-skip reasons
// readable without an errors.New var per message.
type errorString string

func (e errorString) Error() string { return string(e) }

// voiceProbeBadge is the persisted badge shape under the instance metadata's
// credential_probe key.
type voiceProbeBadge struct {
	Status    string `json:"status"`
	CheckedAt string `json:"checked_at"`
}

// voiceProbeNeedsRefresh decides whether one badge warrants a re-probe:
// every degraded or absent state, plus an ok older than the trust window.
// A fresh ok is the only skip.
func voiceProbeNeedsRefresh(badgeRaw json.RawMessage, now time.Time, maxAge time.Duration) bool {
	var badge voiceProbeBadge
	if len(badgeRaw) == 0 || json.Unmarshal(badgeRaw, &badge) != nil {
		return true
	}
	switch badge.Status {
	case credentialProbeInvalid, credentialProbeUnreachable:
		return true
	case credentialProbeOK:
		checkedAt, err := time.Parse(time.RFC3339, badge.CheckedAt)
		if err != nil {
			return true
		}
		return now.Sub(checkedAt) >= maxAge
	default:
		// skipped and anything unknown: retry — a skipped badge was a
		// transient inability to probe, not a verdict.
		return true
	}
}

// RefreshVoiceProbeBadges walks every instance carrying a stored credential,
// re-probes the voice-family ones whose badge is degraded, missing, or older
// than the trust window, and records the fresh outcome. Designed to be
// called from the background refresher tick (cmd/server) and directly from
// tests; the per-instance failure modes are log lines, never a sweep abort.
func (h *Handler) RefreshVoiceProbeBadges(ctx context.Context) {
	runtimes, err := h.Queries.ListRuntimesWithCredentialRef(ctx)
	if err != nil {
		slog.Warn("voice probe refresh: list candidates failed", "error", err)
		return
	}
	now := time.Now()
	refreshed := 0
	for _, rt := range runtimes {
		if ctx.Err() != nil {
			return
		}
		if !h.instanceHasVoiceFamily(ctx, rt) {
			continue
		}
		badgeRaw := runtimeMetadataBag(rt.Metadata)[credentialProbeMetadataKey]
		if !voiceProbeNeedsRefresh(badgeRaw, now, voiceProbeRefreshMaxAge) {
			continue
		}
		if _, err := h.runVoiceCredentialProbe(ctx, rt); err != nil {
			if err != errVoiceProbeNoCredential {
				slog.Warn("voice probe refresh: probe failed", "runtime_id", uuidToString(rt.ID), "error", err)
			}
			continue
		}
		refreshed++
	}
	if refreshed > 0 {
		slog.Info("voice probe refresh round complete", "refreshed", refreshed)
	}
}
