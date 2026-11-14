package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/knowledgegovernance"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// KnowledgeGovernanceRunResponse is the API projection of an advisory run.
// The report stays JSON because its evidence shape is deliberately extensible;
// the client validates it before rendering.
type KnowledgeGovernanceRunResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	DirID         string          `json:"dir_id"`
	TaskID        *string         `json:"task_id,omitempty"`
	AgentID       string          `json:"agent_id"`
	Status        string          `json:"status"`
	Title         string          `json:"title"`
	PromptVersion string          `json:"prompt_version"`
	Trigger       string          `json:"trigger"`
	Detail        json.RawMessage `json:"detail"`
	Report        json.RawMessage `json:"report"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     string          `json:"created_at"`
	FinishedAt    *string         `json:"finished_at,omitempty"`
}

type KnowledgeGovernanceFindingResponse struct {
	ID                  string          `json:"id"`
	RunID               string          `json:"run_id"`
	EntryID             string          `json:"entry_id"`
	EntryKey            string          `json:"entry_key"`
	OriginalContent     string          `json:"original_content"`
	ContentVersion      int32           `json:"content_version"`
	ContentSHA256       string          `json:"content_sha256"`
	Scope               string          `json:"scope"`
	Verdict             string          `json:"verdict"`
	ProblemCategories   json.RawMessage `json:"problem_categories"`
	CounterEvidence     json.RawMessage `json:"counter_evidence"`
	ApplicableScope     string          `json:"applicable_scope"`
	ReplacementEntryID  *string         `json:"replacement_entry_id,omitempty"`
	Recommendation      string          `json:"recommendation"`
	WritebackAuthorized bool            `json:"writeback_authorized"`
	LifecycleEventRef   string          `json:"lifecycle_event_ref"`
	CreatedAt           string          `json:"created_at"`
}

type knowledgeGovernanceRunRow struct {
	ID            pgtype.UUID
	WorkspaceID   pgtype.UUID
	DirID         pgtype.UUID
	TaskID        pgtype.UUID
	AgentID       pgtype.UUID
	Status        string
	Title         string
	PromptVersion string
	Trigger       string
	Detail        []byte
	Report        []byte
	CreatedBy     pgtype.UUID
	CreatedAt     pgtype.Timestamptz
	FinishedAt    pgtype.Timestamptz
}

const knowledgeGovernanceRunColumns = `
id, workspace_id, dir_id, task_id, agent_id, status, title, prompt_version,
trigger, detail, report, created_by, created_at, finished_at`

func scanKnowledgeGovernanceRun(row pgx.Row) (knowledgeGovernanceRunRow, error) {
	var run knowledgeGovernanceRunRow
	err := row.Scan(
		&run.ID, &run.WorkspaceID, &run.DirID, &run.TaskID, &run.AgentID,
		&run.Status, &run.Title, &run.PromptVersion, &run.Trigger, &run.Detail,
		&run.Report, &run.CreatedBy, &run.CreatedAt, &run.FinishedAt,
	)
	return run, err
}

func knowledgeGovernanceRunToResponse(run knowledgeGovernanceRunRow) KnowledgeGovernanceRunResponse {
	resp := KnowledgeGovernanceRunResponse{
		ID:            uuidToString(run.ID),
		WorkspaceID:   uuidToString(run.WorkspaceID),
		DirID:         uuidToString(run.DirID),
		AgentID:       uuidToString(run.AgentID),
		Status:        run.Status,
		Title:         run.Title,
		PromptVersion: run.PromptVersion,
		Trigger:       run.Trigger,
		Detail:        json.RawMessage(run.Detail),
		Report:        json.RawMessage(run.Report),
		CreatedBy:     uuidToString(run.CreatedBy),
		CreatedAt:     run.CreatedAt.Time.UTC().Format(httpTimeFormat),
	}
	if run.TaskID.Valid {
		id := uuidToString(run.TaskID)
		resp.TaskID = &id
	}
	if run.FinishedAt.Valid {
		finished := run.FinishedAt.Time.UTC().Format(httpTimeFormat)
		resp.FinishedAt = &finished
	}
	return resp
}

func (h *Handler) knowledgeGovernanceRun(ctx *http.Request, runID pgtype.UUID) (knowledgeGovernanceRunRow, error) {
	if h.DB == nil {
		return knowledgeGovernanceRunRow{}, errors.New("database executor is not wired")
	}
	workspaceID, err := util.ParseUUID(h.resolveWorkspaceID(ctx))
	if err != nil {
		return knowledgeGovernanceRunRow{}, err
	}
	return scanKnowledgeGovernanceRun(h.DB.QueryRow(ctx.Context(), `SELECT `+knowledgeGovernanceRunColumns+`
FROM knowledge_governance_run WHERE id = $1 AND workspace_id = $2`, runID, workspaceID))
}

// ListKnowledgeGovernanceRuns returns the directory's recent governance runs.
// Reads are member-visible; owner-only operations are registered separately.
func (h *Handler) ListKnowledgeGovernanceRuns(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusInternalServerError, "database executor is not wired")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	var dirID pgtype.UUID
	if raw := r.URL.Query().Get("dir_id"); raw != "" {
		var valid bool
		dirID, valid = parseUUIDOrBadRequest(w, raw, "directory id")
		if !valid {
			return
		}
	}
	rows, err := h.DB.Query(r.Context(), `SELECT `+knowledgeGovernanceRunColumns+`
FROM knowledge_governance_run
WHERE workspace_id = $1 AND ($2::uuid IS NULL OR dir_id = $2)
ORDER BY created_at DESC LIMIT 100`, workspaceID, dirID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge governance runs")
		return
	}
	defer rows.Close()
	runs := make([]KnowledgeGovernanceRunResponse, 0)
	for rows.Next() {
		run, err := scanKnowledgeGovernanceRun(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read knowledge governance run")
			return
		}
		runs = append(runs, knowledgeGovernanceRunToResponse(run))
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge governance runs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// GetKnowledgeGovernanceRun returns a run and its immutable per-entry audit.
func (h *Handler) GetKnowledgeGovernanceRun(w http.ResponseWriter, r *http.Request) {
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "run id")
	if !ok {
		return
	}
	run, err := h.knowledgeGovernanceRun(r, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "knowledge governance run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load knowledge governance run")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
SELECT id, run_id, entry_id, entry_key, original_content, content_version, content_sha256,
scope, verdict, problem_categories, counter_evidence, applicable_scope, replacement_entry_id,
recommendation, writeback_authorized, lifecycle_event_ref, created_at
FROM knowledge_governance_finding
WHERE run_id = $1 AND workspace_id = $2
ORDER BY created_at ASC`, run.ID, run.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load governance findings")
		return
	}
	defer rows.Close()
	findings := make([]KnowledgeGovernanceFindingResponse, 0)
	for rows.Next() {
		var finding KnowledgeGovernanceFindingResponse
		var id, findingRunID, entryID, replacement pgtype.UUID
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(
			&id, &findingRunID, &entryID, &finding.EntryKey, &finding.OriginalContent,
			&finding.ContentVersion, &finding.ContentSHA256, &finding.Scope, &finding.Verdict,
			&finding.ProblemCategories, &finding.CounterEvidence, &finding.ApplicableScope,
			&replacement, &finding.Recommendation, &finding.WritebackAuthorized,
			&finding.LifecycleEventRef, &createdAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read governance finding")
			return
		}
		finding.ID = uuidToString(id)
		finding.RunID = uuidToString(findingRunID)
		finding.EntryID = uuidToString(entryID)
		finding.CreatedAt = createdAt.Time.UTC().Format(httpTimeFormat)
		if replacement.Valid {
			value := uuidToString(replacement)
			finding.ReplacementEntryID = &value
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load governance findings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": knowledgeGovernanceRunToResponse(run), "findings": findings})
}

// StartKnowledgeGovernanceRun creates an asynchronous advisory run. It never
// performs scan, model, or bd work in the HTTP request.
func (h *Handler) StartKnowledgeGovernanceRun(w http.ResponseWriter, r *http.Request) {
	if h.KnowledgeGovernanceRunner == nil {
		writeError(w, http.StatusInternalServerError, "knowledge governance runner is not wired")
		return
	}
	dirID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "directory id")
	if !ok {
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	stats, err := h.KnowledgeGovernanceRunner.RunWorkspace(r.Context(), h.resolveWorkspaceID(r), uuidToString(dirID), actorID, "manual")
	switch {
	case errors.Is(err, knowledgegovernance.ErrNoExecutor):
		writeErrorCode(w, http.StatusConflict, "agent_not_configured", "no configured execution agent is available")
	case errors.Is(err, knowledgegovernance.ErrScopeRunning):
		writeErrorCode(w, http.StatusConflict, "governance_run_active", "a knowledge governance run is already active for this directory")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to start knowledge governance run")
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"run": stats})
	}
}

// CancelKnowledgeGovernanceRun delegates cancellation to the normal task
// lifecycle. A queued task becomes cancelled synchronously; an in-flight task
// is terminal only after the daemon acknowledges it.
func (h *Handler) CancelKnowledgeGovernanceRun(w http.ResponseWriter, r *http.Request) {
	if h.TaskService == nil || h.KnowledgeGovernanceRunner == nil {
		writeError(w, http.StatusInternalServerError, "knowledge governance runner is not wired")
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "run id")
	if !ok {
		return
	}
	run, err := h.knowledgeGovernanceRun(r, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "knowledge governance run not found")
		return
	}
	if err != nil || !run.TaskID.Valid {
		writeError(w, http.StatusConflict, "knowledge governance run has no cancellable task")
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actor, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	outcome, err := h.TaskService.CancelRunByUser(r.Context(), run.TaskID, actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel knowledge governance run")
		return
	}
	if outcome.Task.Status == "cancelled" {
		if _, err := h.KnowledgeGovernanceRunner.ProcessTaskTerminal(r.Context(), outcome.Task, "", "cancelled"); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to settle knowledge governance cancellation")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": outcome.Code, "task_id": uuidToString(outcome.Task.ID)})
}

// RetryKnowledgeGovernanceRun starts a fresh run for the historical run's
// directory. Snapshot and findings are never overwritten.
func (h *Handler) RetryKnowledgeGovernanceRun(w http.ResponseWriter, r *http.Request) {
	if h.KnowledgeGovernanceRunner == nil {
		writeError(w, http.StatusInternalServerError, "knowledge governance runner is not wired")
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "run id")
	if !ok {
		return
	}
	run, err := h.knowledgeGovernanceRun(r, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "knowledge governance run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load knowledge governance run")
		return
	}
	if run.Status == "running" {
		writeErrorCode(w, http.StatusConflict, "governance_run_active", "cancel or wait for the active knowledge governance run")
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	stats, err := h.KnowledgeGovernanceRunner.RunWorkspace(r.Context(), uuidToString(run.WorkspaceID), uuidToString(run.DirID), actorID, "retry")
	if errors.Is(err, knowledgegovernance.ErrScopeRunning) {
		writeErrorCode(w, http.StatusConflict, "governance_run_active", "a knowledge governance run is already active for this directory")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retry knowledge governance run")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": stats})
}

// KnowledgeGovernanceTaskEnqueuer adapts the governance runner to the normal
// no-issue task queue. The runner notifies the daemon after the transaction commits.
func (h *Handler) KnowledgeGovernanceTaskEnqueuer() knowledgegovernance.TaskEnqueuer {
	return func(ctx context.Context, queries *db.Queries, params knowledgegovernance.EnqueueParams) (db.AgentTaskQueue, error) {
		runID, err := util.ParseUUID(params.RunID)
		if err != nil {
			return db.AgentTaskQueue{}, fmt.Errorf("knowledge governance task enqueue: invalid run id: %w", err)
		}
		agentID, err := util.ParseUUID(params.AgentID)
		if err != nil {
			return db.AgentTaskQueue{}, fmt.Errorf("knowledge governance task enqueue: invalid agent id: %w", err)
		}
		runtimeID, err := util.ParseUUID(params.RuntimeID)
		if err != nil {
			return db.AgentTaskQueue{}, fmt.Errorf("knowledge governance task enqueue: invalid runtime id: %w", err)
		}
		originatorID, err := util.ParseUUID(params.OriginatorID)
		if err != nil {
			return db.AgentTaskQueue{}, fmt.Errorf("knowledge governance task enqueue: invalid originator id: %w", err)
		}
		task, err := queries.CreateKnowledgeGovernanceTask(ctx, db.CreateKnowledgeGovernanceTaskParams{
			AgentID: agentID, RuntimeID: runtimeID, Priority: knowledgegovernance.TaskPriority,
			Context: params.Context, OriginatorUserID: originatorID, AccountableUserID: originatorID, RunID: runID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.AgentTaskQueue{}, errors.New("knowledge governance task enqueue refused: agent or runtime is unavailable")
		}
		if err != nil {
			return db.AgentTaskQueue{}, err
		}
		return db.AgentTaskQueue(task), nil
	}
}
