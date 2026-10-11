package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Binding-free voice configuration (RUYI-693, stage D2).
//
// Owner decision 2026-10-11 10:48 (RUYI-686 决策卡 2/3): voice configuration
// resolves through a two-level chain — the agent's override triple
// (voice_provider_id + voice_model + voice_advanced, level-wise atomic)
// first, then the workspace default stored under workspace.settings' "voice"
// key. When both levels are absent (or the resolved provider fails its
// gate), the client falls back to the official vanilla form: no voice entry.
//
// Gate口径 (same decision): only a missing or invalid credential triggers
// the fallback; an "unreachable" probe verdict never blocks initiation —
// the network conclusion is the dial's business, not the entry's (RUYI-619).
// A credential save or rotation re-runs the probe (保存即探针), so changed
// connection parameters naturally re-enter the "first connectivity test"
// state with a fresh verdict overriding the stale one.
//
// The legacy binding path (agent.voice_runtime_id → agent_runtime) stays
// alive as the last bridge level for the RUYI-693 compat window: instances
// that are not manual voice instances were not migrated, and their bound
// agents must not regress. Migrated agents resolve through the chain before
// the bridge is ever consulted.
// ---------------------------------------------------------------------------

// voiceProviderRef is one chain level's provider pointer: which pool entry
// speaks and the per-level model / advanced refinements around it.
type voiceProviderRef struct {
	ProviderID string                     `json:"provider_id"`
	Model      string                     `json:"model,omitempty"`
	Advanced   map[string]json.RawMessage `json:"advanced,omitempty"`
}

// workspaceVoiceSettings is the workspace.settings "voice" bag: the
// workspace-wide default triple and the ASR configuration (workspace-level
// only, available to the whole workspace — there is deliberately no
// agent-level ASR override).
type workspaceVoiceSettings struct {
	Default *voiceProviderRef `json:"default,omitempty"`
	ASR     *voiceProviderRef `json:"asr,omitempty"`
}

// workspaceVoiceSettingsKey is the workspace.settings bag key holding the
// workspaceVoiceSettings document.
const workspaceVoiceSettingsKey = "voice"

// parseWorkspaceVoiceSettings decodes the "voice" bag out of the workspace
// settings JSONB. Absent or malformed degrades to nil: settings is a shared
// bag written by many features, and a foreign malformed key must not take
// voice down — the read path is tolerant, the write path is strict
// (validateWorkspaceVoiceSettings).
func parseWorkspaceVoiceSettings(settings []byte) *workspaceVoiceSettings {
	if len(settings) == 0 {
		return nil
	}
	var bag map[string]json.RawMessage
	if err := json.Unmarshal(settings, &bag); err != nil {
		return nil
	}
	raw, ok := bag[workspaceVoiceSettingsKey]
	if !ok {
		return nil
	}
	var voice workspaceVoiceSettings
	if err := json.Unmarshal(raw, &voice); err != nil {
		return nil
	}
	return &voice
}

// maxVoiceModelLen bounds a stored model string. Real model names are tens of
// bytes; the ceiling stops the settings/override fields from becoming a
// dumping ground, not because real names approach it.
const maxVoiceModelLen = 200

// normalizeVoiceModel prepares a chain model string for the setup frame:
// surrounding whitespace trimmed and a leading "models/" stripped (the
// gateway prepends the prefix itself — a stored prefix would double it).
func normalizeVoiceModel(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "models/")
	return model
}

// validateVoiceProviderRef checks one chain level's document shape. The
// provider-id existence and workspace-scope checks are the caller's (they
// need the database); this is the pure shape contract both the workspace
// settings write path and the agent override write path enforce.
func validateVoiceProviderRef(ref *voiceProviderRef) error {
	if ref == nil {
		return nil
	}
	if strings.TrimSpace(ref.ProviderID) == "" {
		return errVoiceRefProviderRequired
	}
	if _, err := util.ParseUUID(ref.ProviderID); err != nil {
		return errVoiceRefProviderInvalid
	}
	if len(ref.Model) > maxVoiceModelLen {
		return errVoiceRefModelTooLong
	}
	if ref.Advanced != nil {
		// json.RawMessage values only arrive through a decoded object; a
		// non-object document cannot decode into the map at all, so the
		// shape is already guaranteed by the decoder. Nothing else to check.
	}
	return nil
}

// gatewayServableVoiceProviderType maps a pool entry's provider_type to the
// protocol family the voice gateway actually serves. The pool may hold
// entries of every whitelisted type, but the D2 gateway speaks only the
// Gemini BidiGenerateContent dialect: a chain resolution that lands on any
// other type is a capability mismatch and falls back to vanilla, exactly
// like a non-voice runtime family did under the binding model.
func gatewayServableVoiceProviderType(providerType string) (family string, ok bool) {
	if providerType == "gemini" {
		return "gemini_live", true
	}
	return "", false
}

// voiceTarget is what the resolution chain hands the session starter: the
// resolved configuration entity (a pool provider for chain hits, a runtime
// instance for the legacy bridge), the decrypted hop key and the effective
// model / advanced refinements. Exactly one of provider / runtime is set.
type voiceTarget struct {
	provider *db.VoiceProvider
	runtime  *db.AgentRuntime

	apiKey   string
	model    string
	advanced map[string]json.RawMessage

	// configSource names the chain level that won ("agent_override" /
	// "workspace_default" / "legacy_binding") — recorded into the live
	// session snapshot for write-back audits.
	configSource string
}

// sessionEntityID is the entity a live_session row records under
// runtime_instance_id: the pool provider for chain sessions, the instance
// for legacy ones. Plain UUID column, no FK — both id spaces are safe.
func (t *voiceTarget) sessionEntityID() pgtype.UUID {
	if t.provider != nil {
		return t.provider.ID
	}
	return t.runtime.ID
}

// protocolFamily names the dialect the session will speak, for the snapshot.
func (t *voiceTarget) protocolFamily() string {
	if t.provider != nil {
		family, _ := gatewayServableVoiceProviderType(t.provider.ProviderType)
		return family
	}
	return t.runtime.Provider
}

// voiceProviderCredentialKey extracts the secret-store key half of a
// provider's credential_ref ("<provider-uuid>:<key>").
func voiceProviderCredentialKey(vp db.VoiceProvider) (string, bool) {
	if !vp.CredentialRef.Valid {
		return "", false
	}
	ref := vp.CredentialRef.String
	idx := strings.LastIndex(ref, ":")
	if idx < 0 || idx == len(ref)-1 {
		return "", false
	}
	return ref[idx+1:], true
}

// lastVoiceProviderProbeInvalid mirrors lastVoiceProbeInvalid for pool rows:
// the "失效" signal of the gate. A missing or skipped probe never blocks —
// Gemini re-authenticates at dial time and the relay surfaces a provider
// rejection then.
func lastVoiceProviderProbeInvalid(vp db.VoiceProvider) bool {
	var probe struct {
		Status string `json:"status"`
	}
	if raw, ok := runtimeMetadataBag(vp.Metadata)[credentialProbeMetadataKey]; ok && json.Unmarshal(raw, &probe) == nil {
		return probe.Status == "invalid"
	}
	return false
}

// resolveVoiceTarget is the §4.4 rule-3 initiation recheck under the D2
// model: resolve the chain, gate the winner, and only then hand a hop key
// out. Rejection reasons the client maps to the vanilla fallback (greyed
// entry / no voice UI): voice_unconfigured, voice_provider_missing,
// voice_provider_disabled, capability_mismatch, credential_missing,
// credential_invalid — and every legacy-bridge reason the binding gate
// ever produced.
func (h *Handler) resolveVoiceTarget(ctx context.Context, agentRow db.Agent) (*voiceTarget, *voiceGateRejection, error) {
	target, rejection, err := h.resolveVoiceChainTarget(ctx, agentRow)
	if err != nil {
		return nil, nil, err
	}
	// A chain REJECTION is itself the fallback verdict — return it as-is so
	// the client renders the specific reason. Swallowing it here (to hunt
	// for a legacy binding) would misreport a broken override as
	// "unconfigured" and hide the operator-actionable state.
	if target != nil || rejection != nil {
		return target, rejection, nil
	}

	// Legacy bridge (compat window): an agent still bound to an un-migrated
	// runtime instance resolves exactly like the pre-D2 binding gate.
	// Migrated agents never get here — their chain level hits first.
	if agentRow.VoiceRuntimeID.Valid {
		rt, apiKey, rejection, serverErr := h.resolveLegacyVoiceRuntimeTarget(ctx, agentRow)
		if serverErr != nil || rejection != nil {
			return nil, rejection, serverErr
		}
		model := normalizeVoiceModel(runtimeMetadataString(rt, "model"))
		if model == "" {
			model = defaultVoiceModel
		}
		var advanced map[string]json.RawMessage
		if raw, ok := runtimeMetadataBag(rt.Metadata)["advanced"]; ok {
			json.Unmarshal(raw, &advanced)
		}
		return &voiceTarget{
			runtime:      &rt,
			apiKey:       apiKey,
			model:        model,
			advanced:     advanced,
			configSource: "legacy_binding",
		}, nil, nil
	}

	return nil, &voiceGateRejection{"voice_unconfigured", "no voice provider is configured for this agent or workspace"}, nil
}

// resolveVoiceChainTarget resolves the two-level D2 chain. A nil target with
// a nil rejection means "both levels absent" — the caller proceeds to the
// legacy bridge. A target with a rejection means a level hit but failed its
// gate: the rejection IS the fallback verdict (chain results never cascade
// into the next level — a broken override must not silently switch the agent
// to the workspace default, it must fall back to vanilla so the operator
// notices).
func (h *Handler) resolveVoiceChainTarget(ctx context.Context, agentRow db.Agent) (*voiceTarget, *voiceGateRejection, error) {
	var ref *voiceProviderRef
	var source string
	if agentRow.VoiceProviderID.Valid {
		ref = &voiceProviderRef{
			ProviderID: uuidToString(agentRow.VoiceProviderID),
			Model:      agentRow.VoiceModel.String,
		}
		if len(agentRow.VoiceAdvanced) > 0 {
			json.Unmarshal(agentRow.VoiceAdvanced, &ref.Advanced)
		}
		source = "agent_override"
	} else {
		ws, err := h.Queries.GetWorkspace(ctx, agentRow.WorkspaceID)
		if err != nil {
			return nil, nil, err
		}
		if voice := parseWorkspaceVoiceSettings(ws.Settings); voice != nil && voice.Default != nil {
			ref = voice.Default
			source = "workspace_default"
		}
	}
	if ref == nil || strings.TrimSpace(ref.ProviderID) == "" {
		return nil, nil, nil
	}

	providerUUID, err := util.ParseUUID(ref.ProviderID)
	if err != nil {
		// A malformed reference cannot name a real pool row; it resolves to
		// "missing" rather than a 500 — the shape validator keeps new ones
		// out, old ones degrade to the fallback.
		return nil, &voiceGateRejection{"voice_provider_missing", "the configured voice provider no longer exists"}, nil
	}
	provider, err := h.Queries.GetVoiceProvider(ctx, providerUUID)
	if err != nil || provider.WorkspaceID != agentRow.WorkspaceID {
		// Cross-workspace references are an application-integrity violation
		// (no FK, house rule); they resolve as missing, never as usable.
		return nil, &voiceGateRejection{"voice_provider_missing", "the configured voice provider no longer exists"}, nil
	}
	if !provider.Enabled {
		return nil, &voiceGateRejection{"voice_provider_disabled", "the configured voice provider is disabled"}, nil
	}
	if _, servable := gatewayServableVoiceProviderType(provider.ProviderType); !servable {
		return nil, &voiceGateRejection{"capability_mismatch", "this voice provider's type is not supported by the server yet"}, nil
	}

	keyName, ok := voiceProviderCredentialKey(provider)
	if !ok {
		return nil, &voiceGateRejection{"credential_missing", "this voice provider has no API key configured"}, nil
	}
	credential, err := h.Queries.GetRuntimeCredential(ctx, db.GetRuntimeCredentialParams{
		RuntimeInstanceID: provider.ID,
		CredentialKey:     keyName,
	})
	if err != nil {
		return nil, &voiceGateRejection{"credential_missing", "this voice provider's API key is missing"}, nil
	}
	if lastVoiceProviderProbeInvalid(provider) {
		return nil, &voiceGateRejection{"credential_invalid", "this voice provider's API key failed its last connectivity check"}, nil
	}
	if h.RuntimeCredentialBox == nil {
		// Same fail-closed contract as the credential PUT: without the
		// encryption key the server must not fall back to plaintext hops.
		return nil, nil, errVoiceCredentialEncryptionMissing
	}
	plaintext, err := h.RuntimeCredentialBox.Open(credential.SecretEncrypted)
	if err != nil {
		return nil, &voiceGateRejection{"credential_invalid", "this voice provider's stored API key cannot be decrypted"}, nil
	}

	model := normalizeVoiceModel(ref.Model)
	if model == "" {
		model = defaultVoiceModel
	}
	return &voiceTarget{
		provider:     &provider,
		apiKey:       string(plaintext),
		model:        model,
		advanced:     ref.Advanced,
		configSource: source,
	}, nil, nil
}

// validateWorkspaceVoiceSettings enforces the strict write shape for the
// workspace.settings "voice" bag: both documents must be well-formed and
// their provider ids must reference live pool rows of THIS workspace. The
// read path is tolerant (parseWorkspaceVoiceSettings); only writes that
// explicitly carry the key get checked, so features unrelated to voice are
// never blocked by it.
func (h *Handler) validateWorkspaceVoiceSettings(ctx context.Context, workspaceID pgtype.UUID, settings map[string]json.RawMessage) error {
	raw, ok := settings[workspaceVoiceSettingsKey]
	if !ok {
		return nil
	}
	var voice workspaceVoiceSettings
	if err := json.Unmarshal(raw, &voice); err != nil {
		return errVoiceSettingsMalformed
	}
	for _, ref := range []*voiceProviderRef{voice.Default, voice.ASR} {
		if ref == nil {
			continue
		}
		if err := validateVoiceProviderRef(ref); err != nil {
			return err
		}
		providerUUID, err := util.ParseUUID(ref.ProviderID)
		if err != nil {
			return errVoiceRefProviderInvalid
		}
		provider, err := h.Queries.GetVoiceProvider(ctx, providerUUID)
		if err != nil {
			return errVoiceRefProviderUnknown
		}
		if provider.WorkspaceID != workspaceID {
			return errVoiceRefProviderForeign
		}
	}
	return nil
}

// validateAgentVoiceProviderRef validates the D2 override triple's provider
// leg at agent save time (create + PATCH): the entry must exist inside this
// workspace, be enabled, and speak a family the gateway serves today — the
// same facts the session-start chain re-derives. Model is normalized (trim +
// "models/" prefix strip) and length-checked; advanced goes through
// agentVoiceAdvancedRaw. A request carrying model/advanced without a
// provider id is a half-override and is rejected — the triple is level-wise
// atomic.
func (h *Handler) validateAgentVoiceProviderRef(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, requested, model string, advanced json.RawMessage) (pgtype.UUID, pgtype.Text, []byte, bool) {
	if strings.TrimSpace(requested) == "" {
		if strings.TrimSpace(model) != "" || len(advanced) > 0 {
			writeError(w, http.StatusBadRequest, "voice_model/voice_advanced refine a voice_provider_id override; set voice_provider_id first")
			return pgtype.UUID{}, pgtype.Text{}, nil, false
		}
		return pgtype.UUID{}, pgtype.Text{}, nil, true
	}
	providerUUID, ok := parseUUIDOrBadRequest(w, requested, "voice_provider_id")
	if !ok {
		return pgtype.UUID{}, pgtype.Text{}, nil, false
	}
	provider, err := h.Queries.GetVoiceProvider(r.Context(), providerUUID)
	if err != nil || provider.WorkspaceID != workspaceID {
		writeError(w, http.StatusBadRequest, "voice_provider_id does not name a voice provider in this workspace")
		return pgtype.UUID{}, pgtype.Text{}, nil, false
	}
	if !provider.Enabled {
		writeError(w, http.StatusBadRequest, "voice provider is disabled")
		return pgtype.UUID{}, pgtype.Text{}, nil, false
	}
	if _, ok := gatewayServableVoiceProviderType(provider.ProviderType); !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("voice provider type %q cannot be bound as an agent voice override yet", provider.ProviderType))
		return pgtype.UUID{}, pgtype.Text{}, nil, false
	}
	normalized := normalizeVoiceModel(model)
	if len(normalized) > maxVoiceModelLen {
		writeError(w, http.StatusBadRequest, errVoiceRefModelTooLong.Error())
		return pgtype.UUID{}, pgtype.Text{}, nil, false
	}
	advancedRaw, err := agentVoiceAdvancedRaw(advanced)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return pgtype.UUID{}, pgtype.Text{}, nil, false
	}
	var modelText pgtype.Text
	if normalized != "" {
		modelText = pgtype.Text{String: normalized, Valid: true}
	}
	return providerUUID, modelText, advancedRaw, true
}

// agentVoiceAdvancedRaw normalizes the agent override's advanced field for
// storage: an empty or non-object document stores as NULL (not set), an
// object stores as-is. Mirrors how the §4.3 instance PATCH treats advanced.
func agentVoiceAdvancedRaw(raw json.RawMessage) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, errVoiceRefAdvancedNotObject
	}
	return raw, nil
}

// recordVoiceProviderProbeOutcome merges a probe outcome into a pool row's
// metadata bag (保存即探针 and the re-probe endpoint). Failures are logged
// and swallowed: the credential is already stored, and the badge self-heals
// on the next save or probe.
func (h *Handler) recordVoiceProviderProbeOutcome(ctx context.Context, providerID pgtype.UUID, metadata []byte, probe CredentialProbeResult) {
	encoded, err := mergeAgentRuntimeMetadataKey(metadata, credentialProbeMetadataKey, probe)
	if err != nil {
		slog.Warn("merge voice provider probe outcome failed", "voice_provider_id", uuidToString(providerID), "error", err)
		return
	}
	if err := h.Queries.SetVoiceProviderMetadata(ctx, db.SetVoiceProviderMetadataParams{
		ID:       providerID,
		Metadata: encoded,
	}); err != nil {
		slog.Warn("record voice provider probe outcome failed", "voice_provider_id", uuidToString(providerID), "error", err)
	}
}

// clearVoiceProviderProbeOutcome removes the badge (credential DELETE): a
// provider with no key has no probe state to show.
func (h *Handler) clearVoiceProviderProbeOutcome(ctx context.Context, providerID pgtype.UUID, metadata []byte) {
	encoded, err := mergeAgentRuntimeMetadataKey(metadata, credentialProbeMetadataKey, nil)
	if err != nil {
		slog.Warn("clear voice provider probe outcome failed", "voice_provider_id", uuidToString(providerID), "error", err)
		return
	}
	if err := h.Queries.SetVoiceProviderMetadata(ctx, db.SetVoiceProviderMetadataParams{
		ID:       providerID,
		Metadata: encoded,
	}); err != nil {
		slog.Warn("clear voice provider probe outcome failed", "voice_provider_id", uuidToString(providerID), "error", err)
	}
}
