package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// RecordTaskNoAction records the current ordinary issue task's explicit silent
// outcome. It is task-token-only because the marker governs that exact run's
// completion fallback and comment-write fence.
func (h *Handler) RecordTaskNoAction(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "no_action can only be recorded by the running task's credential")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Reason = strings.TrimSpace(sanitizeNullBytes(req.Reason))
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}

	task, ok := h.taskFromRequestHeader(r)
	if !ok || uuidToString(task.AgentID) != r.Header.Get("X-Agent-ID") || task.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	updated, err := h.TaskService.RecordTaskNoAction(r.Context(), task.ID, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTaskNoActionReasonRequired):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrTaskNoActionNotActive),
			errors.Is(err, service.ErrTaskNoActionLeader),
			errors.Is(err, service.ErrTaskNoActionHasOutput):
			writeError(w, http.StatusConflict, err.Error())
		default:
			slog.Warn("record task no_action failed", "task_id", uuidToString(task.ID), "issue_id", uuidToString(issue.ID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to record no_action")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":      uuidToString(updated.ID),
		"issue_id":     uuidToString(updated.IssueID),
		"reason":       updated.NoActionReason.String,
		"no_action_at": timestampToString(updated.NoActionAt),
	})
}
