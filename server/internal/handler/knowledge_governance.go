package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

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

type KnowledgeGovernanceOpinionResponse struct {
	ID                  string `json:"id"`
	DirID               string `json:"dir_id"`
	EntryID             string `json:"entry_id"`
	RunID               string `json:"run_id"`
	FindingID           string `json:"finding_id"`
	Action              string `json:"action"`
	ProposedContent     string `json:"proposed_content"`
	TargetVersion       int32  `json:"target_version"`
	TargetContentSHA256 string `json:"target_content_sha256"`
	Status              string `json:"status"`
	CreatedAt           string `json:"created_at"`
}

func (h *Handler) ListKnowledgeGovernanceOpinions(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `SELECT id, dir_id, entry_id, run_id, finding_id, action, proposed_content, target_version, target_content_sha256, status, created_at
FROM knowledge_governance_opinion WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 100`, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list governance opinions")
		return
	}
	defer rows.Close()
	out := make([]KnowledgeGovernanceOpinionResponse, 0)
	for rows.Next() {
		var item KnowledgeGovernanceOpinionResponse
		var id, dirID, entryID, runID, findingID pgtype.UUID
		var created pgtype.Timestamptz
		if err := rows.Scan(&id, &dirID, &entryID, &runID, &findingID, &item.Action, &item.ProposedContent, &item.TargetVersion, &item.TargetContentSHA256, &item.Status, &created); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read governance opinion")
			return
		}
		item.ID, item.DirID, item.EntryID, item.RunID, item.FindingID = uuidToString(id), uuidToString(dirID), uuidToString(entryID), uuidToString(runID), uuidToString(findingID)
		item.CreatedAt = created.Time.UTC().Format(httpTimeFormat)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list governance opinions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opinions": out})
}

func (h *Handler) DecideKnowledgeGovernanceOpinion(w http.ResponseWriter, r *http.Request) {
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	opinionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "opinion id")
	if !ok {
		return
	}
	action := chi.URLParam(r, "decision")
	if action != "approve" && action != "reject" {
		writeError(w, http.StatusNotFound, "unknown opinion decision")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start opinion decision")
		return
	}
	defer tx.Rollback(r.Context())
	var entryID pgtype.UUID
	var targetVersion int32
	var targetSHA, status string
	if err := tx.QueryRow(r.Context(), `SELECT entry_id, target_version, target_content_sha256, status FROM knowledge_governance_opinion WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, opinionID, workspaceID).Scan(&entryID, &targetVersion, &targetSHA, &status); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "governance opinion not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load governance opinion")
		return
	}
	if status != "pending" {
		writeError(w, http.StatusConflict, "governance opinion is no longer pending")
		return
	}
	if action == "approve" {
		var currentVersion int32
		var currentSHA string
		if err := tx.QueryRow(r.Context(), `SELECT current_version, content_sha256 FROM knowledge_entry WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, entryID, workspaceID).Scan(&currentVersion, &currentSHA); err != nil || currentVersion != targetVersion || currentSHA != targetSHA {
			if _, updateErr := tx.Exec(r.Context(), `UPDATE knowledge_governance_opinion SET status = 'stale', error = 'entry version changed before approval', updated_at = now() WHERE id = $1`, opinionID); updateErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to mark stale governance opinion")
				return
			}
			if err := tx.Commit(r.Context()); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to commit stale governance opinion")
				return
			}
			writeError(w, http.StatusConflict, "knowledge entry changed; governance opinion is stale")
			return
		}
	}
	newStatus := "approved"
	if action == "reject" {
		newStatus = "rejected"
	}
	if _, err := tx.Exec(r.Context(), `UPDATE knowledge_governance_opinion SET status = $2, decided_by = $3, decided_at = now(), updated_at = now() WHERE id = $1`, opinionID, newStatus, actorID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decide governance opinion")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit governance opinion")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": newStatus})
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

type knowledgeGovernanceConfigResponse struct {
	Enabled         bool                                     `json:"enabled"`
	AgentID         string                                   `json:"agent_id"`
	AgentName       string                                   `json:"agent_name"`
	Cadence         string                                   `json:"cadence"`
	Timezone        string                                   `json:"timezone"`
	Version         int32                                    `json:"version"`
	Source          string                                   `json:"source"`
	UpdatedAt       string                                   `json:"updated_at,omitempty"`
	Executable      bool                                     `json:"executable"`
	BlockReason     string                                   `json:"block_reason,omitempty"`
	NextScheduledAt string                                   `json:"next_scheduled_at,omitempty"`
	Audit           []knowledgeGovernanceConfigAuditResponse `json:"audit"`
}

type knowledgeGovernanceConfigAuditResponse struct {
	Version     int32           `json:"version"`
	ActorID     string          `json:"actor_id"`
	BeforeValue json.RawMessage `json:"before_value"`
	AfterValue  json.RawMessage `json:"after_value"`
	CreatedAt   string          `json:"created_at"`
}

type knowledgeGovernanceConfigRow struct {
	Enabled        bool
	AgentID        pgtype.UUID
	Cadence        string
	Timezone       string
	Version        int32
	NextEligibleAt pgtype.Timestamptz
	UpdatedBy      pgtype.UUID
	UpdatedAt      pgtype.Timestamptz
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) governanceConfig(ctx context.Context, workspaceID pgtype.UUID) (knowledgeGovernanceConfigRow, error) {
	var cfg knowledgeGovernanceConfigRow
	err := h.DB.QueryRow(ctx, `
SELECT enabled, agent_id, cadence, timezone, version, next_eligible_at, updated_by, updated_at
FROM knowledge_governance_config WHERE workspace_id = $1`, workspaceID).Scan(
		&cfg.Enabled, &cfg.AgentID, &cfg.Cadence, &cfg.Timezone, &cfg.Version, &cfg.NextEligibleAt, &cfg.UpdatedBy, &cfg.UpdatedAt,
	)
	return cfg, err
}

func (h *Handler) governanceConfigResponse(ctx context.Context, workspaceID pgtype.UUID) (knowledgeGovernanceConfigResponse, error) {
	cfg, err := h.governanceConfig(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return knowledgeGovernanceConfigResponse{
			Enabled: false, Cadence: "daily", Timezone: "UTC", Source: "default", BlockReason: "disabled", Audit: []knowledgeGovernanceConfigAuditResponse{},
		}, nil
	}
	if err != nil {
		return knowledgeGovernanceConfigResponse{}, err
	}
	response := knowledgeGovernanceConfigResponse{
		Enabled: cfg.Enabled, AgentID: uuidToString(cfg.AgentID), Cadence: cfg.Cadence, Timezone: cfg.Timezone,
		Version: cfg.Version, Source: "explicit", Audit: []knowledgeGovernanceConfigAuditResponse{},
	}
	if !cfg.Enabled {
		response.BlockReason = "disabled"
	} else if !cfg.AgentID.Valid {
		response.BlockReason = "agent_not_configured"
	} else if agent, agentErr := h.Queries.GetAgent(ctx, cfg.AgentID); agentErr != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		response.BlockReason = "agent_unavailable"
	} else if !cfg.NextEligibleAt.Valid {
		response.BlockReason = "invalid_schedule"
	} else if next, nextErr := knowledgegovernance.DisplayNextSchedule(h.now(), cfg.Cadence, cfg.Timezone, cfg.NextEligibleAt.Time); nextErr != nil {
		response.BlockReason = "invalid_schedule"
	} else {
		response.AgentName = agent.Name
		response.Executable = true
		response.NextScheduledAt = next.UTC().Format(httpTimeFormat)
	}
	rows, err := h.DB.Query(ctx, `
SELECT config_version, actor_id, before_value, after_value, created_at
FROM knowledge_governance_config_audit
WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 20`, workspaceID)
	if err != nil {
		return knowledgeGovernanceConfigResponse{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var audit knowledgeGovernanceConfigAuditResponse
		var actor pgtype.UUID
		var created pgtype.Timestamptz
		if err := rows.Scan(&audit.Version, &actor, &audit.BeforeValue, &audit.AfterValue, &created); err != nil {
			return knowledgeGovernanceConfigResponse{}, err
		}
		audit.ActorID = uuidToString(actor)
		if created.Valid {
			audit.CreatedAt = created.Time.UTC().Format(httpTimeFormat)
		}
		response.Audit = append(response.Audit, audit)
	}
	return response, rows.Err()
}

// GetKnowledgeGovernanceConfig returns the independent, persisted scheduler
// controls and their recent audit trail. The router limits this to members.
func (h *Handler) GetKnowledgeGovernanceConfig(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	response, err := h.governanceConfigResponse(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge governance config")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// UpdateKnowledgeGovernanceConfig saves only governance controls. It never
// writes retrospective_config, so the two schedule planes cannot silently
// change one another's executor or enabled state.
func (h *Handler) UpdateKnowledgeGovernanceConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  *bool   `json:"enabled"`
		AgentID  *string `json:"agent_id"`
		Cadence  *string `json:"cadence"`
		Timezone *string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start knowledge governance config update")
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck
	// The workspace row serializes first-time inserts as well as updates to an
	// existing config row. Holding both locks until audit insertion commits
	// makes version allocation and its immutable audit record one operation.
	var lockedWorkspace pgtype.UUID
	if err := tx.QueryRow(r.Context(), `SELECT id FROM workspace WHERE id = $1 FOR UPDATE`, workspaceID).Scan(&lockedWorkspace); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	var current knowledgeGovernanceConfigRow
	err = tx.QueryRow(r.Context(), `
SELECT enabled, agent_id, cadence, timezone, version, next_eligible_at, updated_by, updated_at
FROM knowledge_governance_config WHERE workspace_id = $1 FOR UPDATE`, workspaceID).Scan(
		&current.Enabled, &current.AgentID, &current.Cadence, &current.Timezone, &current.Version, &current.NextEligibleAt, &current.UpdatedBy, &current.UpdatedAt,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to read knowledge governance config")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		current = knowledgeGovernanceConfigRow{Cadence: "daily", Timezone: "UTC"}
	}
	enabled, agentID, cadence, timezone := current.Enabled, current.AgentID, current.Cadence, current.Timezone
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.Cadence != nil {
		cadence = *req.Cadence
	}
	if cadence != "daily" && cadence != "weekly" && cadence != "monthly" {
		writeError(w, http.StatusBadRequest, "cadence must be daily, weekly, or monthly")
		return
	}
	if req.Timezone != nil {
		timezone = *req.Timezone
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		writeError(w, http.StatusBadRequest, "timezone must be a valid IANA timezone")
		return
	}
	if req.AgentID != nil {
		if *req.AgentID == "" {
			agentID = pgtype.UUID{}
		} else {
			parsed, parseErr := util.ParseUUID(*req.AgentID)
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "agent_id is not a valid UUID")
				return
			}
			agent, agentErr := h.Queries.GetAgent(r.Context(), parsed)
			if agentErr != nil || uuidToString(agent.WorkspaceID) != uuidToString(workspaceID) {
				writeError(w, http.StatusBadRequest, "agent not found in this workspace")
				return
			}
			if agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
				writeError(w, http.StatusBadRequest, "agent is not runnable")
				return
			}
			agentID = parsed
		}
	}
	if enabled && !agentID.Valid {
		writeError(w, http.StatusBadRequest, "enabling knowledge governance requires a selected agent")
		return
	}
	plannedAt, planErr := knowledgegovernance.NextSchedule(h.now(), cadence, timezone)
	if planErr != nil {
		writeError(w, http.StatusBadRequest, "unable to plan the next governance schedule")
		return
	}
	actorID, actorErr := util.ParseUUID(requestUserID(r))
	if actorErr != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	before, _ := json.Marshal(map[string]any{"enabled": current.Enabled, "agent_id": uuidToString(current.AgentID), "cadence": current.Cadence, "timezone": current.Timezone, "next_eligible_at": current.NextEligibleAt.Time.UTC().Format(httpTimeFormat)})
	after, _ := json.Marshal(map[string]any{"enabled": enabled, "agent_id": uuidToString(agentID), "cadence": cadence, "timezone": timezone, "next_eligible_at": plannedAt.UTC().Format(httpTimeFormat)})
	version := current.Version + 1
	if version < 1 {
		version = 1
	}
	_, err = tx.Exec(r.Context(), `
INSERT INTO knowledge_governance_config (workspace_id, enabled, agent_id, cadence, timezone, version, next_eligible_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (workspace_id) DO UPDATE SET
  enabled = EXCLUDED.enabled, agent_id = EXCLUDED.agent_id, cadence = EXCLUDED.cadence,
  timezone = EXCLUDED.timezone, version = EXCLUDED.version, next_eligible_at = EXCLUDED.next_eligible_at, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		workspaceID, enabled, agentID, cadence, timezone, version, plannedAt, actorID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `
INSERT INTO knowledge_governance_config_audit (workspace_id, config_version, actor_id, before_value, after_value)
VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)`, workspaceID, version, actorID, before, after)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save knowledge governance config")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit knowledge governance config")
		return
	}
	response, err := h.governanceConfigResponse(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read saved knowledge governance config")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

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
