package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/outerloop"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type outerLoopPlanRequest struct {
	Enabled   *bool   `json:"enabled"`
	MaxRounds *int    `json:"max_rounds"`
	Goal      *string `json:"goal"`
}

type outerLoopPlanView struct {
	State      string `json:"state"`
	Goal       string `json:"goal"`
	MaxRounds  int    `json:"max_rounds"`
	Round      int    `json:"round"`
	WaitReason string `json:"wait_reason"`
}

type outerLoopPlanResponse struct {
	Enabled       bool              `json:"enabled"`
	HasPlan       bool              `json:"has_plan"`
	Plan          outerLoopPlanView `json:"plan"`
	IssueRevision int64             `json:"issue_revision"`
}

func (h *Handler) outerLoopIssue(w http.ResponseWriter, r *http.Request) (db.Issue, bool) {
	if !featureflags.OuterLoopEnabled(r.Context(), h.FeatureFlags) {
		writeError(w, http.StatusNotFound, "outer loop is disabled")
		return db.Issue{}, false
	}
	return h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
}

func outerLoopResponse(issue db.Issue) outerLoopPlanResponse {
	metadata := util.JSONObjectOrEmpty(issue.Metadata)
	plan, ok := outerloop.ParsePlan(metadata)
	view := outerLoopPlanView{State: outerloop.StateStopped, MaxRounds: outerloop.DefaultMaxRounds}
	if ok {
		view = outerLoopPlanView{
			State: plan.State, Goal: plan.Goal, MaxRounds: plan.MaxRounds, Round: plan.Round,
		}
	}
	if reason, _ := metadata[outerloop.MetaWaitReason].(string); reason != "" {
		view.WaitReason = reason
	}
	return outerLoopPlanResponse{
		Enabled: ok && plan.State != outerloop.StateStopped, HasPlan: ok, Plan: view, IssueRevision: issue.Revision,
	}
}

func (h *Handler) publishOuterLoopPlanChanged(issue db.Issue) {
	h.publish(protocol.EventIssueMetadataChanged, uuidToString(issue.WorkspaceID), "member", "", map[string]any{
		"issue_id":       uuidToString(issue.ID),
		"metadata":       util.JSONObjectOrEmpty(issue.Metadata),
		"issue_revision": issue.Revision,
	})
}

func (h *Handler) GetOuterLoopPlan(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.outerLoopIssue(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, outerLoopResponse(issue))
}

func (h *Handler) requireOuterLoopMember(w http.ResponseWriter, r *http.Request, issue db.Issue) (pgtype.UUID, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return pgtype.UUID{}, false
	}
	actorType, _ := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	if actorType != "member" {
		writeError(w, http.StatusForbidden, "outer loop control requires a member actor")
		return pgtype.UUID{}, false
	}
	return util.MustParseUUID(userID), true
}

func (h *Handler) PutOuterLoopPlan(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.outerLoopIssue(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireOuterLoopMember(w, r, issue); !ok {
		return
	}
	var req outerLoopPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock outer loop plan")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockIssueDuplicateKey(r.Context(), uuidToString(issue.ID)+":outerloop"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock outer loop plan")
		return
	}
	issue, err = qtx.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	metadata := parseIssueMetadata(issue.Metadata)
	plan, hasPlan := outerloop.ParsePlan(metadata)
	if req.Enabled == nil && req.MaxRounds == nil && req.Goal == nil {
		writeError(w, http.StatusBadRequest, "at least one plan field is required")
		return
	}
	if req.MaxRounds != nil {
		if _, valid := outerloop.ClampMaxRounds(*req.MaxRounds); !valid {
			writeError(w, http.StatusBadRequest, "max_rounds must be between 1 and 1000")
			return
		}
		if hasPlan && *req.MaxRounds < plan.MaxRounds {
			writeError(w, http.StatusBadRequest, "max_rounds only rises")
			return
		}
	}
	if req.Enabled != nil && *req.Enabled && (!issue.AssigneeType.Valid || issue.AssigneeType.String != "agent") {
		writeError(w, http.StatusBadRequest, "an agent assignee is required to enable outer loop")
		return
	}
	state := plan.State
	if !hasPlan {
		state = outerloop.StateStopped
	}
	if req.Enabled != nil {
		if *req.Enabled {
			state = outerloop.StateRunning
		} else {
			state = outerloop.StateStopped
		}
	}
	max := plan.MaxRounds
	if !hasPlan {
		max = outerloop.DefaultMaxRounds
	}
	if req.MaxRounds != nil {
		max = *req.MaxRounds
	}
	goal := plan.Goal
	if req.Goal != nil {
		goal = *req.Goal
	}
	values := []struct {
		key   string
		value any
	}{{outerloop.MetaState, state}, {outerloop.MetaMaxRounds, max}, {outerloop.MetaGoal, goal}}
	if !hasPlan && issue.AssigneeID.Valid {
		values = append(values, struct {
			key   string
			value any
		}{outerloop.MetaAgent, uuidToString(issue.AssigneeID)})
	}
	if req.Enabled != nil && *req.Enabled && state == outerloop.StateRunning && plan.State == outerloop.StateStopped {
		values = append(values, struct {
			key   string
			value any
		}{outerloop.MetaRound, 0})
	}
	updated := issue
	for _, entry := range values {
		raw, _ := json.Marshal(entry.value)
		var err error
		updated, err = qtx.SetIssueMetadataKey(r.Context(), db.SetIssueMetadataKeyParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID, Key: entry.key, Value: raw})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update outer loop plan")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save outer loop plan")
		return
	}
	h.publishOuterLoopPlanChanged(updated)
	writeJSON(w, http.StatusOK, outerLoopResponse(updated))
}

func (h *Handler) FireOuterLoopPlan(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.outerLoopIssue(w, r)
	if !ok {
		return
	}
	actor, ok := h.requireOuterLoopMember(w, r, issue)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock outer loop plan")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockIssueDuplicateKey(r.Context(), uuidToString(issue.ID)+":outerloop"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock outer loop plan")
		return
	}
	issue, err = qtx.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	plan, ok := outerloop.ParsePlan(parseIssueMetadata(issue.Metadata))
	if !ok || plan.State != outerloop.StateRunning {
		writeError(w, http.StatusConflict, "outer loop plan is not running")
		return
	}
	if plan.Round >= plan.MaxRounds {
		writeError(w, http.StatusConflict, "outer loop has reached its round limit")
		return
	}
	if h.TaskService == nil {
		writeError(w, http.StatusServiceUnavailable, "task service is unavailable")
		return
	}
	planAgent, err := util.ParseUUID(plan.AgentID)
	if err != nil {
		planAgent = issue.AssigneeID
	}
	if !planAgent.Valid {
		writeError(w, http.StatusConflict, "outer loop plan has no agent")
		return
	}
	pending, err := qtx.HasPendingTaskForIssueAndAgent(r.Context(), db.HasPendingTaskForIssueAndAgentParams{IssueID: issue.ID, AgentID: planAgent, HeadSha: pgtype.Text{}})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect pending tasks")
		return
	}
	if pending {
		writeJSON(w, http.StatusOK, map[string]any{"dispatched": false, "reason": "pending_task"})
		return
	}
	// The loop belongs to the agent pinned in its metadata, not a potentially
	// newer Issue assignee. This mirrors the terminal/sweep paths in service.
	dispatchIssue := issue
	dispatchIssue.AssigneeID = planAgent
	dispatchIssue.AssigneeType = pgtype.Text{String: "agent", Valid: true}
	if _, err := h.TaskService.EnqueueTaskForIssueWithHandoff(r.Context(), dispatchIssue, outerloop.RoundDoc(plan.Round+1, plan.MaxRounds, plan.Goal), actor); err != nil {
		writeError(w, http.StatusConflict, "failed to dispatch outer loop")
		return
	}
	updated, err := qtx.IncrementOuterLoopRound(r.Context(), db.IncrementOuterLoopRoundParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "outer loop dispatch succeeded but round update failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save outer loop dispatch")
		return
	}
	h.publishOuterLoopPlanChanged(updated)
	writeJSON(w, http.StatusOK, map[string]any{"dispatched": true, "plan": outerLoopResponse(updated).Plan, "issue_revision": updated.Revision})
}

func (h *Handler) StopOuterLoopPlan(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.outerLoopIssue(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireOuterLoopMember(w, r, issue); !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock outer loop plan")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.LockIssueDuplicateKey(r.Context(), uuidToString(issue.ID)+":outerloop"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock outer loop plan")
		return
	}
	issue, err = qtx.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if _, hasPlan := outerloop.ParsePlan(parseIssueMetadata(issue.Metadata)); !hasPlan {
		writeError(w, http.StatusNotFound, "outer loop plan not found")
		return
	}
	updated, err := qtx.SetIssueMetadataKey(r.Context(), db.SetIssueMetadataKeyParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID, Key: outerloop.MetaState, Value: []byte(`"stopped"`)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stop outer loop")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stop outer loop")
		return
	}
	h.publishOuterLoopPlanChanged(updated)
	writeJSON(w, http.StatusOK, outerLoopResponse(updated))
}
