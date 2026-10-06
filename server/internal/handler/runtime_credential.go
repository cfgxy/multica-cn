package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Runtime instance credentials (RUYI-425 §4.5)
//
// A credential VALUE lives only in the server-side secret store
// (runtime_credential.secret_encrypted, AES-256-GCM via the
// MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY box). The agent_runtime row carries
// nothing but the credential_ref pointer ("<instance-uuid>:<key>"), and no
// response ever echoes the value back — the instance payload exposes only the
// coarse CredentialStatus badge. These endpoints are the ONLY way a value
// enters or leaves the store, and both are gated by canEditRuntime.
// ---------------------------------------------------------------------------

// runtimeCredentialKeyPattern keeps the credential_key path segment a single
// safe token: it is half of the credential_ref string and half of the
// runtime_credential primary key. 'api_key' is the only key the Gemini Live
// gateway needs today; the pattern leaves room for future named credentials
// without widening the wire format.
var runtimeCredentialKeyPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// maxRuntimeCredentialLen bounds an accepted credential value. API keys are
// hundreds of bytes at most; the ceiling exists so a client cannot turn the
// secret store into a blob bucket, not because real keys approach it.
const maxRuntimeCredentialLen = 8192

type putRuntimeCredentialRequest struct {
	Value string `json:"value"`
}

// PutRuntimeCredential handles PUT /api/runtimes/{runtimeId}/credentials/{credentialKey}.
// Stores (or rotates) the credential value under its key and points the
// instance's credential_ref at it. Rotation is the same call: the instance ID
// is stable, so existing Agent Context secrets_refs stay valid (§4.5).
func (h *Handler) PutRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	if h.RuntimeCredentialBox == nil {
		// Fail closed exactly like the VCS box: a self-host deployment without
		// MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY must never fall back to storing
		// plaintext.
		writeError(w, http.StatusServiceUnavailable, "runtime credential encryption is not configured on this server (MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY missing)")
		return
	}

	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	credentialKey := strings.TrimSpace(chi.URLParam(r, "credentialKey"))
	if !runtimeCredentialKeyPattern.MatchString(credentialKey) {
		writeError(w, http.StatusBadRequest, "credential key must match [a-z0-9_]{1,64}")
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

	var req putRuntimeCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Value) == "" {
		writeError(w, http.StatusBadRequest, "value is required")
		return
	}
	if len(req.Value) > maxRuntimeCredentialLen {
		writeError(w, http.StatusBadRequest, "value exceeds the maximum credential length")
		return
	}

	sealed, err := h.RuntimeCredentialBox.Seal([]byte(req.Value))
	if err != nil {
		slog.Error("seal runtime credential failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return
	}

	// Single transaction: the secret row and the pointer onto the instance row
	// appear (or rotate) together, so credential_ref can never name a missing
	// credential.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if _, err := qtx.UpsertRuntimeCredential(r.Context(), db.UpsertRuntimeCredentialParams{
		RuntimeInstanceID: runtimeUUID,
		CredentialKey:     credentialKey,
		SecretEncrypted:   sealed,
	}); err != nil {
		slog.Error("store runtime credential failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return
	}
	ref := runtimeID + ":" + credentialKey
	if _, err := qtx.SetAgentRuntimeCredentialRef(r.Context(), db.SetAgentRuntimeCredentialRefParams{
		ID:            runtimeUUID,
		CredentialRef: ptrToText(&ref),
	}); err != nil {
		slog.Error("point credential_ref failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("commit runtime credential failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return
	}

	// §4.3/§4.5: saving a credential on a voice-family instance triggers the
	// connectivity probe. It runs AFTER the commit so it can never block or
	// roll back the save; a failed probe surfaces as the "invalid" badge and
	// the next save re-probes.
	probe := CredentialProbeResult{Status: "skipped"}
	if h.instanceHasVoiceFamily(r.Context(), rt) {
		probe = h.probeVoiceCredential(r.Context(), req.Value)
		if probe.Status != "skipped" {
			h.recordCredentialProbeOutcome(r.Context(), runtimeUUID, rt.Metadata, probe)
		}
		slog.Info("runtime credential probed", "runtime_id", runtimeID, "probe_status", probe.Status, "http_status", probe.HTTPStatus)
	}

	// The response is the badge shape, deliberately value-free — the same
	// masked contract the instance list carries.
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime_id":        runtimeID,
		"credential_key":    credentialKey,
		"credential_status": "configured",
		"probe":             probe,
	})
}

// DeleteRuntimeCredential handles DELETE /api/runtimes/{runtimeId}/credentials/{credentialKey}.
// Removes the stored value and clears the instance's credential_ref in one
// transaction. Idempotent: deleting an absent credential still clears any
// dangling ref and returns 204.
func (h *Handler) DeleteRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	credentialKey := strings.TrimSpace(chi.URLParam(r, "credentialKey"))
	if !runtimeCredentialKeyPattern.MatchString(credentialKey) {
		writeError(w, http.StatusBadRequest, "credential key must match [a-z0-9_]{1,64}")
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

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if _, err := qtx.DeleteRuntimeCredential(r.Context(), db.DeleteRuntimeCredentialParams{
		RuntimeInstanceID: runtimeUUID,
		CredentialKey:     credentialKey,
	}); err != nil {
		slog.Error("delete runtime credential failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}
	if _, err := qtx.ClearAgentRuntimeCredentialRef(r.Context(), runtimeUUID); err != nil {
		slog.Error("clear credential_ref failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("commit runtime credential delete failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}

	// A deleted credential has no probe state left to show; drop the badge
	// key so a future credential starts from a clean tri-state. Failure to
	// clean up is a log line — the next save overwrites the stale outcome.
	h.clearCredentialProbeOutcome(r.Context(), runtimeUUID, rt.Metadata)

	w.WriteHeader(http.StatusNoContent)
}
