package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RUYI-608: the scheduling freeze switch — HTTP surface.
//
// POST/DELETE/GET /api/agents/{id}/scheduling-pause and
// POST/DELETE/GET /api/workspaces/{id}/scheduling-pause.
//
// Authorization (Owner ruling on the issue, 2026-10-09): ONLY a workspace
// owner/admin may freeze or resume scheduling. Agent owners without the
// workspace role, plain members, and every machine credential (mat_ task
// tokens, mcn_ cloud-node PATs, MCP OAuth tokens) are rejected — an agent
// must never be able to pause or resume its own scheduling, which makes
// this endpoint human-admin-only by construction. Reads stay member-visible
// (the agents list / workspace DTO expose the same state to every viewer).
//
// Every state change is fail-closed audited inside the service transaction
// (agent.scheduling_paused / ops.scheduling_paused families) and broadcast
// to workspace realtime subscribers so open UIs refresh without polling.

// pauseSchedulingRequest is the freeze body. reason is the operator's
// free-text note; it lands in scheduling_pause.reason and the audit event
// details verbatim (existing redaction discipline applies at log shipping,
// nothing here is sensitive by construction).
type pauseSchedulingRequest struct {
	Reason string `json:"reason"`
}

type schedulingPauseResponse struct {
	Paused      bool   `json:"paused"`
	Scope       string `json:"scope,omitempty"`
	Reason      string `json:"reason,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	QueuedCount int64  `json:"queued_count"`
}

func schedulingPauseResponseFrom(state service.SchedulingState) schedulingPauseResponse {
	return schedulingPauseResponse{
		Paused:      state.Paused,
		Scope:       state.Scope,
		Reason:      state.Reason,
		CreatedBy:   state.CreatedBy,
		CreatedAt:   state.CreatedAt,
		QueuedCount: state.QueuedCount,
	}
}

func decodePauseSchedulingRequest(w http.ResponseWriter, r *http.Request) (pauseSchedulingRequest, bool) {
	var req pauseSchedulingRequest
	if r.Body == nil {
		return req, true
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	return req, true
}

// authorizeSchedulingControl is the shared write gate: human actor +
// workspace owner/admin. Returns the acting member. Fail-closed order —
// machine-credential backstop FIRST (a mat_/mcn_ request stamps the owning
// human's X-User-ID, so the role check alone would let a compromised agent
// token flip workspace scheduling), then the agent-actor resolution, then
// the role gate.
func (h *Handler) authorizeSchedulingControl(w http.ResponseWriter, r *http.Request, workspaceID string) (db.Member, bool) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "scheduling control is only available to human administrators")
		return db.Member{}, false
	}

	userID := requestUserID(r)
	actorType, _ := h.resolveActor(r, userID, workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not pause or resume scheduling")
		return db.Member{}, false
	}

	return h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
}

// ---- Agent-scoped endpoints ----

func (h *Handler) PauseAgentScheduling(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(agent.WorkspaceID))
	if !ok {
		return
	}
	req, ok := decodePauseSchedulingRequest(w, r)
	if !ok {
		return
	}

	if _, _, err := h.TaskService.PauseAgentScheduling(r.Context(),
		agent.WorkspaceID, agent.ID, req.Reason, member.UserID); err != nil {
		slog.Error("pause agent scheduling failed", "agent_id", uuidToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to pause agent scheduling")
		return
	}
	h.writeAgentSchedulingState(w, r.Context(), agent.WorkspaceID, agent.ID)
}

func (h *Handler) ResumeAgentScheduling(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(agent.WorkspaceID))
	if !ok {
		return
	}

	queued, err := h.TaskService.ResumeAgentScheduling(r.Context(),
		agent.WorkspaceID, agent.ID, member.UserID)
	if err != nil {
		slog.Error("resume agent scheduling failed", "agent_id", uuidToString(agent.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to resume agent scheduling")
		return
	}

	// Resume is idempotent; report the (now flowing) queue depth plus the
	// post-resume pause state, which is false even when no row existed.
	writeJSON(w, http.StatusOK, schedulingPauseResponse{
		Paused:      false,
		QueuedCount: queued,
	})
}

func (h *Handler) GetAgentSchedulingPause(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	h.writeAgentSchedulingState(w, r.Context(), agent.WorkspaceID, agent.ID)
}

// writeAgentSchedulingState reads the effective state through the service
// reader (which resolves the agent-vs-workspace scope precedence) and
// serializes it. Used by both the GET endpoint and the POST response.
func (h *Handler) writeAgentSchedulingState(w http.ResponseWriter, ctx context.Context, workspaceID, agentID pgtype.UUID) {
	state, err := h.TaskService.AgentSchedulingState(ctx, workspaceID, agentID)
	if err != nil {
		slog.Error("read agent scheduling state failed", "agent_id", uuidToString(agentID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read scheduling state")
		return
	}
	writeJSON(w, http.StatusOK, schedulingPauseResponseFrom(state))
}

// ---- Workspace-scoped endpoints ----

func (h *Handler) PauseWorkspaceScheduling(w http.ResponseWriter, r *http.Request) {
	wsID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(wsID))
	if !ok {
		return
	}
	req, ok := decodePauseSchedulingRequest(w, r)
	if !ok {
		return
	}

	if _, _, err := h.TaskService.PauseWorkspaceScheduling(r.Context(),
		wsID, req.Reason, member.UserID); err != nil {
		slog.Error("pause workspace scheduling failed", "workspace_id", uuidToString(wsID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to pause workspace scheduling")
		return
	}
	h.writeWorkspaceSchedulingState(w, r.Context(), wsID)
}

func (h *Handler) ResumeWorkspaceScheduling(w http.ResponseWriter, r *http.Request) {
	wsID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	member, ok := h.authorizeSchedulingControl(w, r, uuidToString(wsID))
	if !ok {
		return
	}

	queued, err := h.TaskService.ResumeWorkspaceScheduling(r.Context(), wsID, member.UserID)
	if err != nil {
		slog.Error("resume workspace scheduling failed", "workspace_id", uuidToString(wsID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to resume workspace scheduling")
		return
	}

	writeJSON(w, http.StatusOK, schedulingPauseResponse{
		Paused:      false,
		QueuedCount: queued,
	})
}

func (h *Handler) GetWorkspaceSchedulingPause(w http.ResponseWriter, r *http.Request) {
	wsID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(wsID)); !ok {
		return
	}
	h.writeWorkspaceSchedulingState(w, r.Context(), wsID)
}

func (h *Handler) writeWorkspaceSchedulingState(w http.ResponseWriter, ctx context.Context, workspaceID pgtype.UUID) {
	state, err := h.TaskService.WorkspaceSchedulingState(ctx, workspaceID)
	if err != nil {
		slog.Error("read workspace scheduling state failed", "workspace_id", uuidToString(workspaceID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read scheduling state")
		return
	}
	writeJSON(w, http.StatusOK, schedulingPauseResponseFrom(state))
}

// ---- Agent DTO enrichment ----

// applySchedulingToAgentResponse fills the freeze fields on one agent
// response: scope resolution mirrors GetSchedulingPauseForAgent (an
// agent-level row is the more specific answer when both levels hold), and
// the queued depth mirrors what the agents-list batch fill reports for the
// same agent — list and detail must agree (RUYI-608 QA P1: the detail
// projection dropped the count).
func (h *Handler) applySchedulingToAgentResponse(ctx context.Context, resp *AgentResponse, workspaceID, agentID pgtype.UUID) {
	scope, paused, err := h.schedulingScopeForAgent(ctx, workspaceID, agentID)
	if err != nil {
		slog.Warn("agent scheduling enrichment failed", "agent_id", uuidToString(agentID), "error", err)
		return
	}
	resp.SchedulingPaused = paused
	resp.SchedulingPausedScope = scope
	q, err := h.Queries.CountQueuedTasksForAgent(ctx, agentID)
	if err != nil {
		slog.Warn("agent scheduling enrichment failed", "agent_id", uuidToString(agentID), "error", err)
		return
	}
	resp.SchedulingQueuedCount = q
}

// applySchedulingToAgentResponses batch-fills the freeze fields for a whole
// workspace's agents with two queries (pause rows + queued counts) — no
// per-agent round trips.
func (h *Handler) applySchedulingToAgentResponses(ctx context.Context, workspaceID pgtype.UUID, resp []AgentResponse) {
	pauseRows, err := h.Queries.ListSchedulingPausesByWorkspace(ctx, workspaceID)
	if err != nil {
		slog.Warn("agents scheduling enrichment failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return
	}
	var wsPaused bool
	agentPaused := map[string]struct{}{}
	for _, row := range pauseRows {
		if row.AgentID.Valid {
			agentPaused[uuidToString(row.AgentID)] = struct{}{}
		} else {
			wsPaused = true
		}
	}

	counts, err := h.Queries.CountQueuedTasksByWorkspacePerAgent(ctx, workspaceID)
	if err != nil {
		slog.Warn("agents scheduling enrichment failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return
	}
	queuedByAgent := make(map[string]int64, len(counts))
	for _, c := range counts {
		queuedByAgent[uuidToString(c.AgentID)] = c.QueuedCount
	}

	for i := range resp {
		_, agentLevel := agentPaused[resp[i].ID]
		switch {
		case agentLevel:
			resp[i].SchedulingPaused = true
			resp[i].SchedulingPausedScope = service.SchedulingPauseScopeAgent
		case wsPaused:
			resp[i].SchedulingPaused = true
			resp[i].SchedulingPausedScope = service.SchedulingPauseScopeWorkspace
		}
		resp[i].SchedulingQueuedCount = queuedByAgent[resp[i].ID]
	}
}

// schedulingScopeForAgent answers (scope, paused) for one agent, honoring
// the same precedence as the SQL fence: any matching row = frozen, and a
// specific agent-level row names the scope.
func (h *Handler) schedulingScopeForAgent(ctx context.Context, workspaceID, agentID pgtype.UUID) (string, bool, error) {
	row, err := h.Queries.GetSchedulingPauseForAgent(ctx, db.GetSchedulingPauseForAgentParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if row.AgentID.Valid {
		return service.SchedulingPauseScopeAgent, true, nil
	}
	return service.SchedulingPauseScopeWorkspace, true, nil
}

// ---- Workspace DTO enrichment ----

// applySchedulingToWorkspaceResponse fills the workspace-level freeze
// fields on the workspace detail response. Agent-level freezes are NOT
// aggregated here — the workspace flag means "the workspace-level switch
// is on"; per-agent state lives on the agents list.
func (h *Handler) applySchedulingToWorkspaceResponse(ctx context.Context, resp *WorkspaceResponse, workspaceID pgtype.UUID) {
	row, err := h.Queries.GetWorkspaceSchedulingPause(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		slog.Warn("workspace scheduling enrichment failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return
	}
	resp.SchedulingPaused = true
	resp.SchedulingPausedReason = row.Reason
	if row.CreatedAt.Valid {
		resp.SchedulingPausedAt = strPtrOrNil(row.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00"))
	}
	q, err := h.Queries.CountQueuedTasksByWorkspace(ctx, workspaceID)
	if err != nil {
		slog.Warn("workspace scheduling enrichment failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return
	}
	resp.SchedulingQueuedCount = q
}

// applySchedulingToWorkspaceResponses batch-fills the workspace-level freeze
// fields for the workspaces list with two queries — the row-level semantics
// mirror applySchedulingToWorkspaceResponse exactly (the flag means the
// workspace-level switch is on; agent-level freezes are not aggregated and
// the queued count is scoped to the freeze row), so list and detail answer
// identically for the same workspace (RUYI-608 QA P1: the list projection
// was never wired and answered false/0 while frozen).
func (h *Handler) applySchedulingToWorkspaceResponses(ctx context.Context, resp []WorkspaceResponse) {
	if len(resp) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(resp))
	byID := make(map[string]*WorkspaceResponse, len(resp))
	for i := range resp {
		byID[resp[i].ID] = &resp[i]
		ids = append(ids, parseUUID(resp[i].ID))
	}

	rows, err := h.Queries.ListWorkspaceSchedulingPauses(ctx, ids)
	if err != nil {
		slog.Warn("workspaces scheduling enrichment failed", "error", err)
		return
	}
	for _, row := range rows {
		ws, ok := byID[uuidToString(row.WorkspaceID)]
		if !ok {
			continue
		}
		ws.SchedulingPaused = true
		ws.SchedulingPausedReason = row.Reason
		if row.CreatedAt.Valid {
			ws.SchedulingPausedAt = strPtrOrNil(row.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00"))
		}
	}

	counts, err := h.Queries.CountQueuedTasksPerWorkspace(ctx, ids)
	if err != nil {
		slog.Warn("workspaces scheduling enrichment failed", "error", err)
		return
	}
	for _, c := range counts {
		if ws, ok := byID[uuidToString(c.WorkspaceID)]; ok {
			ws.SchedulingQueuedCount = c.QueuedCount
		}
	}
}
