package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/multica-ai/multica/server/internal/jevconfig"
)

type jevConfigResponse struct {
	Status          string `json:"status"`
	Endpoint        string `json:"endpoint"`
	Model           string `json:"model"`
	TimeoutMS       int32  `json:"timeout_ms"`
	HasAPIKey       bool   `json:"has_api_key"`
	LastCheckedAt   string `json:"last_checked_at,omitempty"`
	LastCheckOK     *bool  `json:"last_check_ok,omitempty"`
	LastCheckError  string `json:"last_check_error,omitempty"`
	Engine          string `json:"engine,omitempty"`
	EncryptionReady bool   `json:"encryption_ready"`
}

type jevConfigSaveRequest struct {
	Endpoint  string `json:"endpoint"`
	APIKey    string `json:"api_key"`
	Model     string `json:"model"`
	TimeoutMS int32  `json:"timeout_ms"`
}

// GetSelfEvolutionJevConfig returns the workspace JEV status without exposing
// any credential material. It is member-visible; owner middleware protects
// the save, delete and recheck routes.
func (h *Handler) GetSelfEvolutionJevConfig(w http.ResponseWriter, r *http.Request) {
	if h.JevConfig == nil {
		writeJSON(w, http.StatusOK, jevConfigResponse{
			Status:          jevconfig.StatusUnconfigured,
			TimeoutMS:       1500,
			EncryptionReady: false,
		})
		return
	}
	h.writeJevConfig(w, r)
}

// PutSelfEvolutionJevConfig saves one endpoint and immediately records its
// connectivity state. An offline endpoint is retained so the owner can see
// the exact safe reason and fix it later; it never blocks proposal handling.
func (h *Handler) PutSelfEvolutionJevConfig(w http.ResponseWriter, r *http.Request) {
	if h.JevConfig == nil {
		h.writeJevConfigUnavailable(w)
		return
	}
	var req jevConfigSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TimeoutMS == 0 {
		req.TimeoutMS = 1500
	}
	if _, err := h.JevConfig.Save(r.Context(), parseUUID(h.resolveWorkspaceID(r)), jevconfig.SaveInput{
		BaseURL: req.Endpoint, APIKey: req.APIKey, Model: req.Model, TimeoutMS: req.TimeoutMS,
	}); err != nil {
		switch {
		case errors.Is(err, jevconfig.ErrIncompleteConfig), errors.Is(err, jevconfig.ErrInvalidEndpoint), errors.Is(err, jevconfig.ErrInvalidTimeout):
			writeError(w, http.StatusBadRequest, "invalid JEV configuration")
		default:
			writeError(w, http.StatusInternalServerError, "failed to save JEV configuration")
		}
		return
	}
	h.writeJevConfig(w, r)
}

func (h *Handler) DeleteSelfEvolutionJevConfig(w http.ResponseWriter, r *http.Request) {
	if h.JevConfig == nil {
		h.writeJevConfigUnavailable(w)
		return
	}
	if err := h.JevConfig.Delete(r.Context(), parseUUID(h.resolveWorkspaceID(r))); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete JEV configuration")
		return
	}
	h.writeJevConfig(w, r)
}

func (h *Handler) RecheckSelfEvolutionJevConfig(w http.ResponseWriter, r *http.Request) {
	if h.JevConfig == nil {
		h.writeJevConfigUnavailable(w)
		return
	}
	if _, err := h.JevConfig.Recheck(r.Context(), parseUUID(h.resolveWorkspaceID(r))); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to recheck JEV configuration")
		return
	}
	h.writeJevConfig(w, r)
}

func (h *Handler) writeJevConfig(w http.ResponseWriter, r *http.Request) {
	state, err := h.JevConfig.State(r.Context(), parseUUID(h.resolveWorkspaceID(r)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read JEV configuration")
		return
	}
	resp := jevConfigResponse{
		Status:          state.Status,
		Endpoint:        state.BaseURL,
		Model:           state.Model,
		TimeoutMS:       state.TimeoutMS,
		HasAPIKey:       state.HasAPIKey,
		LastCheckError:  state.LastCheckError,
		Engine:          state.LastEngine,
		EncryptionReady: true,
	}
	if state.LastCheckedAt.Valid {
		resp.LastCheckedAt = state.LastCheckedAt.Time.UTC().Format(httpTimeFormat)
	}
	if state.LastCheckOK.Valid {
		ok := state.LastCheckOK.Bool
		resp.LastCheckOK = &ok
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) writeJevConfigUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "JEV configuration is unavailable: MULTICA_SELF_EVOLUTION_SECRET_KEY is not set")
}
