package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// ConsumeQueuedTasksForIssue implements POST /api/issues/{id}/consume-queued-tasks
// (RUYI-48): the running job's formal channel for absorbing the issue's queued
// messages into its own run.
//
// Why this exists: the (issue, agent) pending unique index covers only
// queued/dispatched, so a comment mention arriving while a task is RUNNING
// enqueues a fresh queued task. The running agent can read that message
// mid-run and act on it, but the queue never learns — the task stays queued in
// the UI and re-executes the same work once the active run completes. This
// endpoint lets the running job declare "I have read and am handling the
// queued messages": the queued rows are cancelled (their execution merges into
// the in-progress run) and their comments join the run's delivered receipt, so
// completion reconcile does not replay them.
//
// Authentication is deliberately narrow: only a mat_ task-token credential
// (X-Actor-Source: task_token, stamped by the Auth middleware) may consume, and
// only for the bound (agent, task) pair — the act is meaningful only for the
// run that actually holds those messages. Human JWT / mul_ PAT credentials get
// 403: releasing queue entries is the running job's decision, not an editor's.
func (h *Handler) ConsumeQueuedTasksForIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "queued messages can only be consumed by the running task's credential")
		return
	}
	taskID := r.Header.Get("X-Task-ID")
	agentID := r.Header.Get("X-Agent-ID")
	if taskID == "" || agentID == "" {
		writeError(w, http.StatusForbidden, "task token binding missing")
		return
	}

	consuming, err := h.Queries.GetAgentTask(r.Context(), parseUUID(taskID))
	if err != nil || uuidToString(consuming.AgentID) != agentID || uuidToString(consuming.IssueID) != uuidToString(issue.ID) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	result, err := h.TaskService.ConsumeQueuedTasksForRunningTask(r.Context(), consuming.ID)
	if err != nil {
		if errors.Is(err, service.ErrTaskNotConsumable) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.Warn("consume queued tasks failed",
			"task_id", taskID, "issue_id", uuidToString(issue.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to consume queued tasks")
		return
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	tasks := make([]AgentTaskResponse, len(result.Tasks))
	for i := range result.Tasks {
		tasks[i] = taskToResponse(result.Tasks[i], workspaceID)
	}
	commentIDs := make([]string, len(result.CommentIDs))
	for i := range result.CommentIDs {
		commentIDs[i] = uuidToString(result.CommentIDs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"consumed_tasks":       tasks,
		"consumed_comment_ids": commentIDs,
	})
}
