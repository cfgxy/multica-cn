package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Voice provider pool (RUYI-693, stage D2).
//
// The workspace-level pool of voice suppliers the binding-free resolution
// chain reads. A provider row owns a display name, a provider type, an
// enabled switch and — through credential_ref — exactly one credential in
// the shared runtime_credential secret store. No plaintext key ever touches
// a row or a response: the value enters through the credential PUT (which
// runs the connectivity probe, 保存即探针) and never leaves again.
//
// Pool management is workspace admin state: reads are member-visible (the
// settings page renders for everyone), writes sit in the router's admin
// group — same split as the workspace settings themselves. Deletes unbind
// rather than block: referencing agent overrides are cleared and the
// workspace settings' voice references nulled inside the delete transaction,
// so the resolution chain can never dangle after a confirmed delete.
// ---------------------------------------------------------------------------

var (
	errVoiceCredentialEncryptionMissing = errors.New("runtime credential encryption is not configured")
	errVoiceSettingsMalformed           = errors.New("voice settings must be a JSON object with optional default/asr documents")
	errVoiceRefProviderRequired         = errors.New("voice provider_id is required")
	errVoiceRefProviderInvalid          = errors.New("voice provider_id must be a provider id")
	errVoiceRefProviderUnknown          = errors.New("voice provider does not exist in this workspace")
	errVoiceRefProviderForeign          = errors.New("voice provider belongs to another workspace")
	errVoiceRefModelTooLong             = errors.New("voice model exceeds the maximum length")
	errVoiceRefAdvancedNotObject        = errors.New("voice advanced must be a JSON object")
)

// runVoiceProviderProbe failures: operator-actionable "nothing to probe"
// states surfaced as 400 by the probe endpoint.
var (
	errVoiceProbeNoCredential  = errors.New("voice provider has no stored API key to probe")
	errVoiceProbeUndecryptable = errors.New("voice provider's stored API key cannot be decrypted")
)

// voiceProviderTypePattern keeps the create-time type check aligned with the
// 961 whitelist CHECK (NOT VALID) without round-tripping the DB error. The
// resolution chain independently falls back to vanilla for types the gateway
// cannot serve yet, so widening this set is safe before the gateway catches
// up — the entry stays visible, voice just stays on the official form.
var voiceProviderTypePattern = regexp.MustCompile(`^(gemini|chatgpt)$`)

// maxVoiceProviderNameLen bounds the display name (same shape as the manual
// instance cap: display names carry no machine-host convention).
const maxVoiceProviderNameLen = 200

type CreateVoiceProviderRequest struct {
	DisplayName  string `json:"display_name"`
	ProviderType string `json:"provider_type"`
}

type UpdateVoiceProviderRequest struct {
	DisplayName *string `json:"display_name"`
	Enabled     *bool   `json:"enabled"`
}

// VoiceProviderResponse is the pool payload. credential_ref carries no
// secret material (it is a pointer); the coarse credential_status badge is
// the only key-state surface, same contract as the instance payload.
type VoiceProviderResponse struct {
	ID               string                 `json:"id"`
	WorkspaceID      string                 `json:"workspace_id"`
	DisplayName      string                 `json:"display_name"`
	ProviderType     string                 `json:"provider_type"`
	Enabled          bool                   `json:"enabled"`
	CredentialStatus string                 `json:"credential_status"`
	Probe            *CredentialProbeResult `json:"credential_probe,omitempty"`
	SourceRuntimeID  string                 `json:"source_runtime_id,omitempty"`
	CreatedAt        string                 `json:"created_at"`
	UpdatedAt        string                 `json:"updated_at"`
}

// voiceProviderCredentialStatus reproduces the instance badge's three coarse
// states from the row's own state: a credential_ref with a live secret row
// is "configured", a ref without one is "missing", nothing is "not_configured".
func (h *Handler) voiceProviderCredentialStatus(ctx context.Context, vp db.VoiceProvider) string {
	keyName, ok := voiceProviderCredentialKey(vp)
	if !ok {
		return "not_configured"
	}
	if _, err := h.Queries.GetRuntimeCredential(ctx, db.GetRuntimeCredentialParams{
		RuntimeInstanceID: vp.ID,
		CredentialKey:     keyName,
	}); err != nil {
		return "missing"
	}
	return "configured"
}

func voiceProviderToResponse(ctx context.Context, h *Handler, vp db.VoiceProvider) VoiceProviderResponse {
	resp := VoiceProviderResponse{
		ID:               uuidToString(vp.ID),
		WorkspaceID:      uuidToString(vp.WorkspaceID),
		DisplayName:      vp.DisplayName,
		ProviderType:     vp.ProviderType,
		Enabled:          vp.Enabled,
		CredentialStatus: h.voiceProviderCredentialStatus(ctx, vp),
		SourceRuntimeID:  uuidToString(vp.SourceRuntimeID),
		CreatedAt:        timestampToString(vp.CreatedAt),
		UpdatedAt:        timestampToString(vp.UpdatedAt),
	}
	if raw, ok := runtimeMetadataBag(vp.Metadata)[credentialProbeMetadataKey]; ok {
		var probe CredentialProbeResult
		if json.Unmarshal(raw, &probe) == nil && probe.Status != "" {
			resp.Probe = &probe
		}
	}
	return resp
}

// ListVoiceProviders handles GET /api/workspaces/{id}/voice-providers —
// member-visible so the settings page renders for everyone.
func (h *Handler) ListVoiceProviders(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	providers, err := h.Queries.ListVoiceProvidersByWorkspace(r.Context(), wsUUID)
	if err != nil {
		slog.Error("list voice providers failed", "workspace_id", wsUUID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list voice providers")
		return
	}
	resp := make([]VoiceProviderResponse, 0, len(providers))
	for _, vp := range providers {
		resp = append(resp, voiceProviderToResponse(r.Context(), h, vp))
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateVoiceProvider handles POST /api/workspaces/{id}/voice-providers
// (admin group). The pool row is born without a key; the client stores it
// through the credential PUT, which probes it (保存即探针).
func (h *Handler) CreateVoiceProvider(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req CreateVoiceProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}
	if len(name) > maxVoiceProviderNameLen {
		writeError(w, http.StatusBadRequest, "display_name exceeds the maximum length")
		return
	}
	if !voiceProviderTypePattern.MatchString(req.ProviderType) {
		writeError(w, http.StatusBadRequest, "provider_type must be one of: gemini, chatgpt")
		return
	}
	vp, err := h.Queries.CreateVoiceProvider(r.Context(), db.CreateVoiceProviderParams{
		WorkspaceID:  wsUUID,
		DisplayName:  name,
		ProviderType: req.ProviderType,
		CreatedBy:    parseUUID(userID),
	})
	if err != nil {
		slog.Error("create voice provider failed", "workspace_id", uuidToString(wsUUID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create voice provider")
		return
	}
	writeJSON(w, http.StatusCreated, voiceProviderToResponse(r.Context(), h, vp))
}

// loadWorkspaceVoiceProvider resolves the providerId URL param and verifies
// the row belongs to the workspace-scoped route — application-layer
// integrity (house rule: no FKs), same check the chain re-derives at
// session start.
func (h *Handler) loadWorkspaceVoiceProvider(w http.ResponseWriter, r *http.Request, workspaceID string) (db.VoiceProvider, bool) {
	providerUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "providerId"), "voice provider id")
	if !ok {
		return db.VoiceProvider{}, false
	}
	vp, err := h.Queries.GetVoiceProvider(r.Context(), providerUUID)
	if err != nil || uuidToString(vp.WorkspaceID) != workspaceID {
		writeError(w, http.StatusNotFound, "voice provider not found")
		return db.VoiceProvider{}, false
	}
	return vp, true
}

// UpdateVoiceProvider handles PATCH /api/workspaces/{id}/voice-providers/{providerId}
// (admin group): display name and the enabled switch. Disabling a provider
// is not a credential operation — the stored key and its probe badge stay,
// so re-enabling restores the exact prior state.
func (h *Handler) UpdateVoiceProvider(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	vp, ok := h.loadWorkspaceVoiceProvider(w, r, workspaceID)
	if !ok {
		return
	}
	var req UpdateVoiceProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	params := db.UpdateVoiceProviderParams{ID: vp.ID}
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" {
			writeError(w, http.StatusBadRequest, "display_name cannot be empty")
			return
		}
		if len(name) > maxVoiceProviderNameLen {
			writeError(w, http.StatusBadRequest, "display_name exceeds the maximum length")
			return
		}
		params.DisplayName = ptrToText(&name)
	}
	if req.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *req.Enabled, Valid: true}
	}
	updated, err := h.Queries.UpdateVoiceProvider(r.Context(), params)
	if err != nil {
		slog.Error("update voice provider failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update voice provider")
		return
	}
	writeJSON(w, http.StatusOK, voiceProviderToResponse(r.Context(), h, updated))
}

// DeleteVoiceProvider handles DELETE /api/workspaces/{id}/voice-providers/{providerId}
// (admin group). One transaction clears everything the resolution chain
// could dangle on: agent override triples, the workspace settings' voice
// references, the secret-store rows, and the row itself.
func (h *Handler) DeleteVoiceProvider(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	vp, ok := h.loadWorkspaceVoiceProvider(w, r, workspaceID)
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete voice provider")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	cleared, err := qtx.ClearAgentVoiceProvider(r.Context(), vp.ID)
	if err != nil {
		slog.Error("clear agent voice provider overrides failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete voice provider")
		return
	}
	if err := h.clearVoiceProviderSettingsRefs(r.Context(), qtx, vp); err != nil {
		slog.Error("clear voice provider settings refs failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete voice provider")
		return
	}
	if _, err := qtx.DeleteRuntimeCredentialsByInstance(r.Context(), vp.ID); err != nil {
		slog.Error("delete voice provider credentials failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete voice provider")
		return
	}
	if err := qtx.DeleteVoiceProvider(r.Context(), vp.ID); err != nil {
		slog.Error("delete voice provider failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete voice provider")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("commit voice provider delete failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete voice provider")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":             uuidToString(vp.ID),
		"cleared_agents": cleared,
	})
}

// clearVoiceProviderSettingsRefs nulls the workspace settings' voice
// references to the deleted provider. The settings bag is shared state: the
// rewrite preserves every non-voice key and only touches the referencing
// document fields.
func (h *Handler) clearVoiceProviderSettingsRefs(ctx context.Context, qtx *db.Queries, vp db.VoiceProvider) error {
	ws, err := qtx.GetWorkspace(ctx, vp.WorkspaceID)
	if err != nil {
		return err
	}
	if len(ws.Settings) == 0 {
		return nil
	}
	bag := map[string]json.RawMessage{}
	if err := json.Unmarshal(ws.Settings, &bag); err != nil {
		return nil // foreign malformed settings: nothing voice-shaped to clear
	}
	raw, ok := bag[workspaceVoiceSettingsKey]
	if !ok {
		return nil
	}
	var voice map[string]json.RawMessage
	if err := json.Unmarshal(raw, &voice); err != nil {
		return nil
	}
	providerID := uuidToString(vp.ID)
	changed := false
	for _, level := range []string{"default", "asr"} {
		var ref struct {
			ProviderID string `json:"provider_id"`
		}
		if err := json.Unmarshal(voice[level], &ref); err != nil {
			continue
		}
		if ref.ProviderID != providerID {
			continue
		}
		delete(voice, level)
		changed = true
	}
	if !changed {
		return nil
	}
	if len(voice) == 0 {
		delete(bag, workspaceVoiceSettingsKey)
	} else if encoded, err := json.Marshal(voice); err == nil {
		bag[workspaceVoiceSettingsKey] = encoded
	} else {
		return err
	}
	encoded, err := json.Marshal(bag)
	if err != nil {
		return err
	}
	_, err = qtx.UpdateWorkspace(ctx, db.UpdateWorkspaceParams{
		ID:       vp.WorkspaceID,
		Settings: encoded,
	})
	return err
}

// PutVoiceProviderCredential handles PUT
// /api/workspaces/{id}/voice-providers/{providerId}/credentials/{credentialKey}
// (admin group). Stores (or rotates) the key and points the row's
// credential_ref at it — then probes the fresh key (保存即探针): the badge
// always reflects the key that is stored right now, which is what makes a
// changed connection parameter re-enter the first-connectivity-test state.
func (h *Handler) PutVoiceProviderCredential(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	vp, ok := h.loadWorkspaceVoiceProvider(w, r, workspaceID)
	if !ok {
		return
	}
	if !h.storeVoiceProviderCredential(w, r, vp, chi.URLParam(r, "credentialKey")) {
		return // response already written
	}
	fresh, err := h.Queries.GetVoiceProvider(r.Context(), vp.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return
	}
	writeJSON(w, http.StatusOK, voiceProviderToResponse(r.Context(), h, fresh))
}

// storeVoiceProviderCredential mirrors PutRuntimeCredential's transaction
// shape (secret row + pointer appear together) for pool rows, then runs the
// probe. The probe NEVER blocks the save — the credential is committed
// before it runs, and an unrecordable outcome is a log line.
func (h *Handler) storeVoiceProviderCredential(w http.ResponseWriter, r *http.Request, vp db.VoiceProvider, credentialKey string) bool {
	if h.RuntimeCredentialBox == nil {
		// Fail closed exactly like the instance PUT: a self-host deployment
		// without the encryption key must never fall back to plaintext.
		writeError(w, http.StatusServiceUnavailable, "runtime credential encryption is not configured on this server (MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY missing)")
		return false
	}
	if !runtimeCredentialKeyPattern.MatchString(credentialKey) {
		writeError(w, http.StatusBadRequest, "credential key must match [a-z0-9_]{1,64}")
		return false
	}
	var req putRuntimeCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if strings.TrimSpace(req.Value) == "" {
		writeError(w, http.StatusBadRequest, "value is required")
		return false
	}
	if len(req.Value) > maxRuntimeCredentialLen {
		writeError(w, http.StatusBadRequest, "value exceeds the maximum credential length")
		return false
	}
	sealed, err := h.RuntimeCredentialBox.Seal([]byte(req.Value))
	if err != nil {
		slog.Error("seal voice provider credential failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return false
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return false
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if _, err := qtx.UpsertRuntimeCredential(r.Context(), db.UpsertRuntimeCredentialParams{
		RuntimeInstanceID: vp.ID,
		CredentialKey:     credentialKey,
		SecretEncrypted:   sealed,
	}); err != nil {
		slog.Error("store voice provider credential failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return false
	}
	ref := uuidToString(vp.ID) + ":" + credentialKey
	if _, err := qtx.SetVoiceProviderCredentialRef(r.Context(), db.SetVoiceProviderCredentialRefParams{
		ID:            vp.ID,
		CredentialRef: ptrToText(&ref),
	}); err != nil {
		slog.Error("point voice provider credential_ref failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return false
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("commit voice provider credential failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store credential")
		return false
	}

	probe := h.probeVoiceCredential(r.Context(), req.Value)
	h.recordVoiceProviderProbeOutcome(r.Context(), vp.ID, vp.Metadata, probe)
	return true
}

// DeleteVoiceProviderCredential handles DELETE
// /api/workspaces/{id}/voice-providers/{providerId}/credentials/{credentialKey}
// (admin group): removes the secret row and the pointer, and clears the
// badge — a provider with no key has no probe state to show. The chain
// answers credential_missing afterwards, which is the vanilla fallback.
func (h *Handler) DeleteVoiceProviderCredential(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	vp, ok := h.loadWorkspaceVoiceProvider(w, r, workspaceID)
	if !ok {
		return
	}
	credentialKey := chi.URLParam(r, "credentialKey")
	if !runtimeCredentialKeyPattern.MatchString(credentialKey) {
		writeError(w, http.StatusBadRequest, "credential key must match [a-z0-9_]{1,64}")
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
		RuntimeInstanceID: vp.ID,
		CredentialKey:     credentialKey,
	}); err != nil {
		slog.Error("delete voice provider credential failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}
	if _, err := qtx.SetVoiceProviderCredentialRef(r.Context(), db.SetVoiceProviderCredentialRefParams{
		ID:            vp.ID,
		CredentialRef: pgtype.Text{},
	}); err != nil {
		slog.Error("clear voice provider credential_ref failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("commit voice provider credential delete failed", "voice_provider_id", uuidToString(vp.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}

	h.clearVoiceProviderProbeOutcome(r.Context(), vp.ID, vp.Metadata)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// RunVoiceProviderProbe handles POST
// /api/workspaces/{id}/voice-providers/{providerId}/credential-probe (admin
// group): the server runs the models.list probe now and records the outcome.
// The request body is never read — under no contract does a client verdict
// become badge state (RUYI-692 D1's rule, carried over to the pool).
func (h *Handler) RunVoiceProviderProbe(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	vp, ok := h.loadWorkspaceVoiceProvider(w, r, workspaceID)
	if !ok {
		return
	}
	probe, err := h.runVoiceProviderProbe(r.Context(), vp)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"voice_provider_id": uuidToString(vp.ID),
		"probe":             probe,
	})
}

// runVoiceProviderProbe executes one server-side probe against the
// provider's stored key and records the outcome on the badge. The error
// return covers "nothing to probe" (no ref / missing secret row) — surfaced
// as 400, operator-actionable state.
func (h *Handler) runVoiceProviderProbe(ctx context.Context, vp db.VoiceProvider) (CredentialProbeResult, error) {
	skip := CredentialProbeResult{Status: credentialProbeSkipped, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	keyName, ok := voiceProviderCredentialKey(vp)
	if !ok {
		return skip, errVoiceProbeNoCredential
	}
	credential, err := h.Queries.GetRuntimeCredential(ctx, db.GetRuntimeCredentialParams{
		RuntimeInstanceID: vp.ID,
		CredentialKey:     keyName,
	})
	if err != nil {
		return skip, errVoiceProbeNoCredential
	}
	if h.RuntimeCredentialBox == nil {
		return skip, errVoiceCredentialEncryptionMissing
	}
	plaintext, err := h.RuntimeCredentialBox.Open(credential.SecretEncrypted)
	if err != nil {
		return skip, errVoiceProbeUndecryptable
	}
	probe := h.probeVoiceCredential(ctx, string(plaintext))
	h.recordVoiceProviderProbeOutcome(ctx, vp.ID, vp.Metadata, probe)
	return probe, nil
}
