package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Autopilot parameter-definition CRUD (RUYI-641 stage A). Definitions are the
// autopilot's calling contract: they are read by any member who can view the
// autopilot and replaced as a whole set by writers — a PUT, not per-row
// PATCHing, because the set carries cross-field invariants (unique names,
// enum defaults, template references) that only hold for the set as a whole.

type AutopilotParamResponse struct {
	ID           string   `json:"id"`
	AutopilotID  string   `json:"autopilot_id"`
	Name         string   `json:"name"`
	ParamType    string   `json:"param_type"`
	Description  string   `json:"description"`
	Required     bool     `json:"required"`
	DefaultValue *string  `json:"default_value"`
	EnumOptions  []string `json:"enum_options"`
	Position     int32    `json:"position"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type AutopilotParamsResponse struct {
	Params []AutopilotParamResponse `json:"params"`
}

type ReplaceAutopilotParamsRequest struct {
	Params []service.AutopilotParamInput `json:"params"`
}

func autopilotParamToResponse(p db.AutopilotParam) AutopilotParamResponse {
	var defaultValue *string
	if p.DefaultValue.Valid {
		v := p.DefaultValue.String
		defaultValue = &v
	}
	enumOptions := p.EnumOptions
	if enumOptions == nil {
		enumOptions = []string{}
	}
	return AutopilotParamResponse{
		ID:           uuidToString(p.ID),
		AutopilotID:  uuidToString(p.AutopilotID),
		Name:         p.Name,
		ParamType:    p.ParamType,
		Description:  p.Description,
		Required:     p.Required,
		DefaultValue: defaultValue,
		EnumOptions:  enumOptions,
		Position:     p.Position,
		CreatedAt:    p.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt:    p.UpdatedAt.Time.UTC().Format(time.RFC3339),
	}
}

func (h *Handler) ListAutopilotParams(w http.ResponseWriter, r *http.Request) {
	autopilot, ok := h.loadAutopilotInWorkspace(w, r, chi.URLParam(r, "id"), h.resolveWorkspaceID(r))
	if !ok {
		return
	}
	defs, err := h.Queries.ListAutopilotParams(r.Context(), autopilot.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autopilot params")
		return
	}
	resp := make([]AutopilotParamResponse, len(defs))
	for i, def := range defs {
		resp[i] = autopilotParamToResponse(def)
	}
	writeJSON(w, http.StatusOK, AutopilotParamsResponse{Params: resp})
}

// ReplaceAutopilotParams swaps the whole definition set in one transaction
// (delete + insert, position = array order) and re-validates the autopilot's
// templates against the NEW name set first: removing a parameter a template
// still references would leave a token that passes no validation and emits
// literally at trigger time.
func (h *Handler) ReplaceAutopilotParams(w http.ResponseWriter, r *http.Request) {
	autopilot, ok := h.loadAutopilotInWorkspace(w, r, chi.URLParam(r, "id"), h.resolveWorkspaceID(r))
	if !ok {
		return
	}
	if !h.requireAutopilotWrite(w, r, autopilot, h.resolveWorkspaceID(r)) {
		return
	}

	var req ReplaceAutopilotParamsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := service.ValidateAutopilotParamInputs(req.Params); err != nil {
		writeAutopilotParamError(w, err)
		return
	}

	// Templates must keep interpolating under the new set: reject the replace
	// when the issue-title template or the task instructions reference a name
	// the new set no longer defines. Empty/absent templates validate trivially.
	names := templateParamNamesFromInputs(req.Params)
	for _, text := range []string{autopilot.IssueTitleTemplate.String, autopilot.Description.String} {
		if err := service.ValidateTemplateTokens(text, names); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to replace autopilot params")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if err := writeAutopilotParamDefs(r.Context(), qtx, autopilot.WorkspaceID, autopilot.ID, req.Params); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to replace autopilot params")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to replace autopilot params")
		return
	}

	defs, err := h.Queries.ListAutopilotParams(r.Context(), autopilot.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autopilot params")
		return
	}
	resp := make([]AutopilotParamResponse, len(defs))
	for i, def := range defs {
		resp[i] = autopilotParamToResponse(def)
	}
	writeJSON(w, http.StatusOK, AutopilotParamsResponse{Params: resp})
}

// writeAutopilotParamError maps a validation failure to its 400 body. The
// reason names the offending parameter — never a submitted value.
func writeAutopilotParamError(w http.ResponseWriter, err error) {
	var paramErr *service.AutopilotParamError
	if errors.As(err, &paramErr) {
		writeError(w, http.StatusBadRequest, paramErr.Reason)
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to validate params")
}

// writeAutopilotParamDefs deletes and re-inserts the definition set inside an
// open transaction, position = array order. Callers own commit/rollback.
func writeAutopilotParamDefs(ctx context.Context, qtx *db.Queries, workspaceID, autopilotID pgtype.UUID, inputs []service.AutopilotParamInput) error {
	if err := qtx.DeleteAutopilotParams(ctx, autopilotID); err != nil {
		return err
	}
	for i, in := range inputs {
		// A nil slice encodes as NULL and the column is NOT NULL — normalize
		// to the empty set the wire omits.
		enumOptions := in.EnumOptions
		if enumOptions == nil {
			enumOptions = []string{}
		}
		if _, err := qtx.CreateAutopilotParam(ctx, db.CreateAutopilotParamParams{
			WorkspaceID:  workspaceID,
			AutopilotID:  autopilotID,
			Name:         in.Name,
			ParamType:    in.ParamType,
			Description:  in.Description,
			Required:     in.Required,
			DefaultValue: inputDefaultToText(in.DefaultValue),
			EnumOptions:  enumOptions,
			Position:     int32(i),
		}); err != nil {
			return err
		}
	}
	return nil
}

func templateParamNamesFromInputs(inputs []service.AutopilotParamInput) map[string]bool {
	names := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		names[in.Name] = true
	}
	return names
}

func inputDefaultToText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
