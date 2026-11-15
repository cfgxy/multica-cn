package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/service"
)

// RUYI-941: Quiz scheduling is independently controllable from ordinary task
// scheduling. Reads are member-visible; writes use the same human owner/admin
// gate as scheduling-pause and are audited by TaskService.
type promptQuizScheduleRequest struct {
	Enabled *bool  `json:"enabled"`
	Reason  string `json:"reason"`
}

type promptQuizSchedulePayload struct {
	Enabled        bool   `json:"enabled"`
	Scope          string `json:"scope"`
	HasOverride    bool   `json:"has_override"`
	Reason         string `json:"reason,omitempty"`
	UpdatedBy      string `json:"updated_by,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	QueuedCount    int64  `json:"queued_count"`
	RunningCount   int64  `json:"running_count"`
	CancelledCount int64  `json:"cancelled_count,omitempty"`
}

func promptQuizScheduleResponse(state service.PromptQuizScheduleState, cancelled int64) promptQuizSchedulePayload {
	return promptQuizSchedulePayload{
		Enabled:        state.Enabled,
		Scope:          state.Scope,
		HasOverride:    state.HasOverride,
		Reason:         state.Reason,
		UpdatedBy:      state.UpdatedBy,
		UpdatedAt:      state.UpdatedAt,
		QueuedCount:    state.QueuedCount,
		RunningCount:   state.RunningCount,
		CancelledCount: cancelled,
	}
}

func decodePromptQuizScheduleRequest(w http.ResponseWriter, r *http.Request) (promptQuizScheduleRequest, bool) {
	var req promptQuizScheduleRequest
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return req, false
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return req, false
	}
	return req, true
}

func (h *Handler) GetWorkspacePromptQuizSchedule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(workspaceID)); !ok {
		return
	}
	state, err := h.TaskService.PromptQuizScheduleState(r.Context(), workspaceID, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read Quiz schedule")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizScheduleResponse(state, 0))
}

func (h *Handler) PutWorkspacePromptQuizSchedule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(workspaceID))
	if !ok {
		return
	}
	req, ok := decodePromptQuizScheduleRequest(w, r)
	if !ok {
		return
	}
	change, err := h.TaskService.SetPromptQuizSchedule(r.Context(), workspaceID, nil, *req.Enabled, req.Reason, member.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update Quiz schedule")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizScheduleResponse(change.State, change.CancelledCount))
}

func (h *Handler) DeleteWorkspacePromptQuizSchedule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(workspaceID))
	if !ok {
		return
	}
	change, err := h.TaskService.RestorePromptQuizSchedule(r.Context(), workspaceID, nil, member.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore Quiz schedule")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizScheduleResponse(change.State, change.CancelledCount))
}

func (h *Handler) GetAgentPromptQuizSchedule(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	state, err := h.TaskService.PromptQuizScheduleState(r.Context(), agent.WorkspaceID, &agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read Quiz schedule")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizScheduleResponse(state, 0))
}

func (h *Handler) PutAgentPromptQuizSchedule(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(agent.WorkspaceID))
	if !ok {
		return
	}
	req, ok := decodePromptQuizScheduleRequest(w, r)
	if !ok {
		return
	}
	change, err := h.TaskService.SetPromptQuizSchedule(r.Context(), agent.WorkspaceID, &agent.ID, *req.Enabled, req.Reason, member.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update Quiz schedule")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizScheduleResponse(change.State, change.CancelledCount))
}

func (h *Handler) DeleteAgentPromptQuizSchedule(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(agent.WorkspaceID))
	if !ok {
		return
	}
	change, err := h.TaskService.RestorePromptQuizSchedule(r.Context(), agent.WorkspaceID, &agent.ID, member.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore Quiz schedule")
		return
	}
	writeJSON(w, http.StatusOK, promptQuizScheduleResponse(change.State, change.CancelledCount))
}
