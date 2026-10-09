package handler

// Self-evolution per-workspace model service config (RUYI-551). The resolver
// (internal/selfevconfig) owns the priority chain and the mandatory
// validation; this file is the HTTP skin around it. Two rules shape the
// surface: writes fail closed (503) when the deployment key is absent, and
// the API key never appears in any response — has_api_key is a boolean and
// validation messages arrive pre-masked from the resolver.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/selfevconfig"
	"github.com/multica-ai/multica/server/pkg/promptperplexity"
)

// modelConfigOverride is the owner-saved module config as the API reports it.
// The key itself is deliberately absent — has_api_key is all a client ever
// learns about it.
type modelConfigOverride struct {
	BaseURL             string `json:"base_url"`
	Model               string `json:"model"`
	HasAPIKey           bool   `json:"has_api_key"`
	ScoringEnabled      bool   `json:"scoring_enabled"`
	LastValidatedAt     string `json:"last_validated_at,omitempty"`
	LastValidationOK    *bool  `json:"last_validation_ok,omitempty"`
	LastValidationError string `json:"last_validation_error,omitempty"`
}

// modelConfigResolved is what a run in this workspace would use right now.
type modelConfigResolved struct {
	Status string `json:"status"` // ok | unconfigured | error | disabled
	Source string `json:"source"` // module_config | deploy_default | "" when unconfigured
	Model  string `json:"model,omitempty"`
}

type modelConfigResponse struct {
	// Override is null when the workspace has no owner-saved config; the
	// resolved block is then the deploy default (or unconfigured).
	Override *modelConfigOverride `json:"override"`
	Resolved modelConfigResolved  `json:"resolved"`
	// ScoringEnabled is the effective retrospective-scoring switch: the saved
	// one when an override exists, on otherwise.
	ScoringEnabled bool `json:"scoring_enabled"`
	// EncryptionReady is false on a deployment without
	// MULTICA_SELF_EVOLUTION_SECRET_KEY: writes are refused (503) and the
	// resolved view is the deploy default.
	EncryptionReady bool `json:"encryption_ready"`
}

type modelConfigSaveRequest struct {
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	Model          string `json:"model"`
	ScoringEnabled *bool  `json:"scoring_enabled"`
}

// GetSelfEvolutionModelConfig — GET /api/self-evolution/model-config
func (h *Handler) GetSelfEvolutionModelConfig(w http.ResponseWriter, r *http.Request) {
	if h.SelfEvolution == nil {
		// Degraded deployment: reads stay truthful about what actually runs
		// (the deploy-wide default) and flag that the card cannot take saves.
		writeJSON(w, http.StatusOK, modelConfigResponse{
			Resolved:        h.deployModelConfigState(),
			ScoringEnabled:  true,
			EncryptionReady: false,
		})
		return
	}
	resp, ok := h.readModelConfig(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// PutSelfEvolutionModelConfig — PUT /api/self-evolution/model-config
// Validation is mandatory whenever credentials are present: a save that
// cannot reach the model is refused, persists nothing, and 422 carries the
// classified, masked reason. An empty api_key keeps the stored key.
func (h *Handler) PutSelfEvolutionModelConfig(w http.ResponseWriter, r *http.Request) {
	if h.SelfEvolution == nil {
		h.writeConfigUnavailable(w)
		return
	}
	var req modelConfigSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	scoring := true
	if req.ScoringEnabled != nil {
		scoring = *req.ScoringEnabled
	}
	in := selfevconfig.SaveInput{BaseURL: req.BaseURL, APIKey: req.APIKey, Model: req.Model, ScoringEnabled: scoring}
	_, failure, err := h.SelfEvolution.Save(r.Context(), parseUUID(h.resolveWorkspaceID(r)), in)
	if err != nil {
		if errors.Is(err, selfevconfig.ErrIncompleteCreds) {
			writeError(w, http.StatusBadRequest, "base_url 与 model 必须同时提供，或同时留空（仅保存启用开关）")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save model config")
		return
	}
	if failure != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"ok":         false,
			"error_kind": string(failure.Kind),
			"message":    failure.Message,
		})
		return
	}
	resp, ok := h.readModelConfig(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// DeleteSelfEvolutionModelConfig — DELETE /api/self-evolution/model-config
// Restores the deploy default: drops the workspace override and invalidates
// the resolver cache so the next run resolves fresh.
func (h *Handler) DeleteSelfEvolutionModelConfig(w http.ResponseWriter, r *http.Request) {
	if h.SelfEvolution == nil {
		h.writeConfigUnavailable(w)
		return
	}
	if err := h.SelfEvolution.RestoreDefault(r.Context(), parseUUID(h.resolveWorkspaceID(r))); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore the deploy default")
		return
	}
	resp, ok := h.readModelConfig(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ValidateSelfEvolutionModelConfig — POST /api/self-evolution/model-config/validate
// An empty body revalidates the stored config and persists the outcome (the
// status card's re-check button); a body carrying credentials previews a
// not-yet-saved config without touching the store.
func (h *Handler) ValidateSelfEvolutionModelConfig(w http.ResponseWriter, r *http.Request) {
	if h.SelfEvolution == nil {
		h.writeConfigUnavailable(w)
		return
	}
	var req modelConfigSaveRequest
	if body := r.Body; body != nil {
		dec := json.NewDecoder(body)
		if err := dec.Decode(&req); err != nil && err.Error() != "EOF" {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	var (
		out selfevconfig.ValidationOutcome
		err error
	)
	if req.BaseURL == "" && req.Model == "" && req.APIKey == "" {
		out, err = h.SelfEvolution.RevalidateStored(r.Context(), parseUUID(h.resolveWorkspaceID(r)))
	} else {
		in := selfevconfig.SaveInput{BaseURL: req.BaseURL, APIKey: req.APIKey, Model: req.Model, ScoringEnabled: true}
		out, err = h.SelfEvolution.ValidateOnly(r.Context(), parseUUID(h.resolveWorkspaceID(r)), in)
	}
	if err != nil {
		if errors.Is(err, selfevconfig.ErrIncompleteCreds) {
			writeError(w, http.StatusBadRequest, "base_url 与 model 必须同时提供")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to validate model config")
		return
	}
	resp := map[string]any{
		"ok":         out.OK,
		"error_kind": string(out.Kind),
		"message":    out.Message,
	}
	if !out.ValidatedAt.IsZero() {
		resp["validated_at"] = out.ValidatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

// readModelConfig composes the GET view from the resolver. ok=false means the
// error has already been written.
func (h *Handler) readModelConfig(w http.ResponseWriter, r *http.Request) (modelConfigResponse, bool) {
	ws := parseUUID(h.resolveWorkspaceID(r))
	ctx := r.Context()
	resp := modelConfigResponse{ScoringEnabled: true, EncryptionReady: true}

	state, err := h.SelfEvolution.ScoringState(ctx, ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read model config")
		return resp, false
	}
	resp.Resolved = modelConfigResolved{Status: state.Status, Source: state.EffectiveSource, Model: state.Model}

	row, has, err := h.SelfEvolution.Override(ctx, ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read model config")
		return resp, false
	}
	if has {
		ov := &modelConfigOverride{
			BaseURL:             row.BaseUrl.String,
			Model:               row.Model.String,
			HasAPIKey:           len(row.ApiKeyEncrypted) > 0,
			ScoringEnabled:      row.ScoringEnabled,
			LastValidationError: row.LastValidationError,
		}
		if row.LastValidatedAt.Valid {
			ov.LastValidatedAt = row.LastValidatedAt.Time.UTC().Format(time.RFC3339)
		}
		if row.LastValidationOk.Valid {
			vok := row.LastValidationOk.Bool
			ov.LastValidationOK = &vok
		}
		resp.Override = ov
		resp.ScoringEnabled = row.ScoringEnabled
	}
	return resp, true
}

// deployModelConfigState is the resolver-less fallback view: what actually
// runs is the deploy-wide default, so that is what the API reports.
func (h *Handler) deployModelConfigState() modelConfigResolved {
	if h.LLM != nil && h.LLM.Enabled() {
		return modelConfigResolved{Status: "ok", Source: selfevconfig.SourceDeployDefault, Model: h.LLM.DefaultModel()}
	}
	return modelConfigResolved{Status: "unconfigured"}
}

func (h *Handler) writeConfigUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "自进化模块配置未启用：需要设置 MULTICA_SELF_EVOLUTION_SECRET_KEY 并重启服务")
}

// PerplexityGeneratorFor adapts the resolver to the D3 scorer's per-workspace
// generator seam: nil (unconfigured, deploy off, or the scoring switch off)
// means "no generator for this workspace", which the rollup treats as skip.
func PerplexityGeneratorFor(r *selfevconfig.Resolver) func(context.Context, pgtype.UUID) promptperplexity.Generator {
	if r == nil {
		return nil
	}
	return func(ctx context.Context, ws pgtype.UUID) promptperplexity.Generator {
		target, err := r.ResolveRunTarget(ctx, ws)
		if err != nil || target.Client == nil || !target.ScoringEnabled {
			return nil
		}
		return target.Client
	}
}
