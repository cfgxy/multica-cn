// Package knowledgegovernance runs evidence-led reviews of one mirrored
// knowledge directory through the normal issue-less agent task queue.
package knowledgegovernance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	TaskKind      = "knowledge_governance"
	PromptVersion = "knowledge-governance-v1"
	TaskPriority  = int32(0)
)

var (
	ErrNoExecutor   = errors.New("knowledge governance: no configured execution agent")
	ErrScopeRunning = errors.New("knowledge governance: a run is already active for this scope")
)

type EntrySnapshot struct {
	ID             string `json:"id"`
	Key            string `json:"key"`
	Content        string `json:"content"`
	ContentSHA256  string `json:"content_sha256"`
	CurrentVersion int32  `json:"current_version"`
	LifecycleState string `json:"lifecycle_state"`
}

type TaskContext struct {
	Kind          string          `json:"kind"`
	RunID         string          `json:"run_id"`
	WorkspaceID   string          `json:"workspace_id"`
	DirID         string          `json:"dir_id"`
	Scope         string          `json:"scope"`
	PromptVersion string          `json:"prompt_version"`
	Entries       []EntrySnapshot `json:"entries"`
}

func ParseTaskContext(raw []byte) (TaskContext, bool) {
	var tc TaskContext
	if len(raw) == 0 || json.Unmarshal(raw, &tc) != nil || tc.Kind != TaskKind || tc.PromptVersion != PromptVersion {
		return TaskContext{}, false
	}
	for _, id := range []string{tc.RunID, tc.WorkspaceID, tc.DirID} {
		if _, err := util.ParseUUID(id); err != nil {
			return TaskContext{}, false
		}
	}
	return tc, true
}

func PromptFromContext(raw []byte) (string, bool) {
	tc, ok := ParseTaskContext(raw)
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString("You are running a knowledge governance review for a Multica workspace. Return exactly one JSON object in your final message. The server rejects prose and reports that omit required evidence.\n\n")
	fmt.Fprintf(&b, "Run ID: %s\nWorkspace ID: %s\nDirectory ID: %s\nScope: %s\nPrompt version: %s\n\n", tc.RunID, tc.WorkspaceID, tc.DirID, tc.Scope, tc.PromptVersion)
	b.WriteString("Hard boundaries:\n")
	b.WriteString("- Do not create, update, comment on, or change any Issue, PR, code, configuration, or knowledge entry. Do not call lifecycle or writeback endpoints. This run is advisory only.\n")
	b.WriteString("- A model judgment is never evidence. Every conclusion requires locatable counter-evidence with source type, immutable reference, observation time, and a relevant excerpt. Missing evidence means needs_review.\n")
	b.WriteString("- Review evidence newest to oldest: Owner corrections first, then Issues and PRs, then current code, then runtime records. Check each entry and cross-entry semantic conflicts for false premises, hidden conflicts, semantic duplicates, obsolete operating rules, historical facts presented as current rules, and unsupported certainty.\n")
	b.WriteString("- Do not authorize writeback. writeback_authorized must always be false. An Owner must later make a separate lifecycle decision; only its guarded daemon writeback may modify a managed active library.\n\n")
	b.WriteString("Entries in scope (each needs exactly one finding, with the exact supplied content, version, and SHA):\n")
	for _, entry := range tc.Entries {
		fmt.Fprintf(&b, "- id=%s key=%q version=%d sha256=%s lifecycle=%s\n  content: %q\n", entry.ID, entry.Key, entry.CurrentVersion, entry.ContentSHA256, entry.LifecycleState, entry.Content)
	}
	b.WriteString(`

Output contract:
{
  "findings":[{
    "entry_id":"UUID","key":"exact key","original_content":"exact content","content_version":1,"content_sha256":"exact SHA","scope":"scope","verdict":"valid|revised|deprecated|superseded|duplicate|invalid|needs_review","problem_categories":["obsolete_operation"],"counter_evidence":[{"source_type":"owner_correction|issue|pull_request|code|runtime_record","immutable_ref":"immutable URL, SHA, or identifier","observed_at":"RFC3339","excerpt":"relevant evidence excerpt"}],"applicable_scope":"where this conclusion applies","replacement_entry_id":"UUID or empty","recommendation":"specific non-binding next step","writeback_authorized":false,"lifecycle_event_ref":"existing lifecycle event id or empty"
  }],
  "retained_coverage":[{"entry_id":"UUID of every valid entry","coverage_evidence":{"source_type":"code","immutable_ref":"...","observed_at":"RFC3339","excerpt":"why this remains covered"},"sampled_counterevidence":{"source_type":"issue","immutable_ref":"...","observed_at":"RFC3339","excerpt":"a sampled attempt to refute it"}}],
  "retrieval_regressions":[{"entry_id":"UUID","expected":"expected recall outcome","observed":"actual result when tested","passed":false,"not_run_reason":"required when no authorized writeback occurred"}],
  "backup_reference":"backup or rollback reference, or an explicit statement that no writeback was authorized"
}
`)
	return b.String(), true
}

type Evidence struct {
	SourceType   string `json:"source_type"`
	ImmutableRef string `json:"immutable_ref"`
	ObservedAt   string `json:"observed_at"`
	Excerpt      string `json:"excerpt"`
}

type Finding struct {
	EntryID             string     `json:"entry_id"`
	Key                 string     `json:"key"`
	OriginalContent     string     `json:"original_content"`
	ContentVersion      int32      `json:"content_version"`
	ContentSHA256       string     `json:"content_sha256"`
	Scope               string     `json:"scope"`
	Verdict             string     `json:"verdict"`
	ProblemCategories   []string   `json:"problem_categories"`
	CounterEvidence     []Evidence `json:"counter_evidence"`
	ApplicableScope     string     `json:"applicable_scope"`
	ReplacementEntryID  string     `json:"replacement_entry_id"`
	Recommendation      string     `json:"recommendation"`
	WritebackAuthorized bool       `json:"writeback_authorized"`
	LifecycleEventRef   string     `json:"lifecycle_event_ref"`
}

type RetainedCoverage struct {
	EntryID                string   `json:"entry_id"`
	CoverageEvidence       Evidence `json:"coverage_evidence"`
	SampledCounterevidence Evidence `json:"sampled_counterevidence"`
}

type RetrievalRegression struct {
	EntryID      string `json:"entry_id"`
	Expected     string `json:"expected"`
	Observed     string `json:"observed"`
	Passed       bool   `json:"passed"`
	NotRunReason string `json:"not_run_reason"`
}

type RunOutput struct {
	Findings             []Finding             `json:"findings"`
	RetainedCoverage     []RetainedCoverage    `json:"retained_coverage"`
	RetrievalRegressions []RetrievalRegression `json:"retrieval_regressions"`
	BackupReference      string                `json:"backup_reference"`
}

type EnqueueParams struct {
	RunID        string
	AgentID      string
	RuntimeID    string
	OriginatorID string
	Context      []byte
}

// TaskEnqueuer persists a governance task through the run creation transaction.
// The caller notifies the daemon only after that transaction commits.
type TaskEnqueuer func(context.Context, *db.Queries, EnqueueParams) (db.AgentTaskQueue, error)

type TaskNotifier func(context.Context, db.AgentTaskQueue)

type Runner struct {
	DB interface {
		db.DBTX
		Begin(context.Context) (pgx.Tx, error)
	}
	Queries *db.Queries
	Enqueue TaskEnqueuer
	Notify  TaskNotifier
	Now     func() time.Time
}

type RunStats struct {
	RunID  string `json:"run_id"`
	TaskID string `json:"task_id,omitempty"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ScheduleTarget identifies one directory and the stable workspace manager
// whose identity makes the system-initiated task accountable and auditable.
type ScheduleTarget struct {
	WorkspaceID string
	DirID       string
	ActorID     string
}

// ListScheduleTargets returns live mirrored directories with at least one
// entry. Directories without a current owner or admin are excluded by the SQL
// join because their system tasks would have no accountable user.
func (r *Runner) ListScheduleTargets(ctx context.Context) ([]ScheduleTarget, error) {
	rows, err := r.DB.Query(ctx, `
SELECT d.workspace_id, d.id AS dir_id, manager.user_id AS actor_id
FROM knowledge_dir d
JOIN LATERAL (
    SELECT user_id
    FROM member
    WHERE workspace_id = d.workspace_id AND role IN ('owner', 'admin')
    ORDER BY created_at ASC
    LIMIT 1
) manager ON TRUE
WHERE d.removed = FALSE
  AND EXISTS (SELECT 1 FROM knowledge_entry e WHERE e.dir_id = d.id)
ORDER BY d.workspace_id ASC, d.created_at ASC, d.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: list schedule targets: %w", err)
	}
	defer rows.Close()
	targets := []ScheduleTarget{}
	for rows.Next() {
		var workspaceID, dirID, actorID pgtype.UUID
		if err := rows.Scan(&workspaceID, &dirID, &actorID); err != nil {
			return nil, fmt.Errorf("knowledge governance: scan schedule target: %w", err)
		}
		targets = append(targets, ScheduleTarget{
			WorkspaceID: util.UUIDToString(workspaceID),
			DirID:       util.UUIDToString(dirID),
			ActorID:     util.UUIDToString(actorID),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("knowledge governance: iterate schedule targets: %w", err)
	}
	return targets, nil
}

// ReconcileStaleRuns is the scheduler backstop for runs whose task reached a
// terminal state without the task lifecycle hook, or was never linked.
func (r *Runner) ReconcileStaleRuns(ctx context.Context) (int64, error) {
	terminalResult, err := r.DB.Exec(ctx, `
UPDATE knowledge_governance_run r
SET status = CASE WHEN t.status = 'cancelled' THEN 'cancelled' ELSE 'failed' END,
    detail = COALESCE(r.detail, '{}'::jsonb) || jsonb_build_object('reason', 'task_not_completed'),
    finished_at = now()
FROM agent_task_queue t
WHERE r.task_id = t.id
  AND r.status = 'running'
  AND t.status IN ('failed', 'cancelled')`)
	if err != nil {
		return 0, fmt.Errorf("knowledge governance: reconcile terminal runs: %w", err)
	}
	unenqueuedResult, err := r.DB.Exec(ctx, `
UPDATE knowledge_governance_run
SET status = 'failed',
    detail = COALESCE(detail, '{}'::jsonb) || jsonb_build_object('reason', 'task_never_enqueued'),
    finished_at = now()
WHERE status = 'running'
  AND task_id IS NULL
  AND created_at < now() - interval '10 minutes'`)
	if err != nil {
		return terminalResult.RowsAffected(), fmt.Errorf("knowledge governance: reconcile unenqueued runs: %w", err)
	}
	if r.Queries == nil {
		return terminalResult.RowsAffected() + unenqueuedResult.RowsAffected(), errors.New("knowledge governance: runner queries are not wired")
	}
	rows, err := r.DB.Query(ctx, `
	SELECT t.id
	FROM knowledge_governance_run r
	JOIN agent_task_queue t ON t.id = r.task_id
	WHERE t.status = 'completed'
	  AND (
	    r.status = 'running'
	    OR (r.status = 'failed' AND r.report->>'reason' = 'report_persistence_failed')
	  )`)
	if err != nil {
		return terminalResult.RowsAffected() + unenqueuedResult.RowsAffected(), fmt.Errorf("knowledge governance: find completed runs: %w", err)
	}
	defer rows.Close()
	var completedTaskIDs []pgtype.UUID
	for rows.Next() {
		var taskID pgtype.UUID
		if err := rows.Scan(&taskID); err != nil {
			return terminalResult.RowsAffected() + unenqueuedResult.RowsAffected(), fmt.Errorf("knowledge governance: scan completed run: %w", err)
		}
		completedTaskIDs = append(completedTaskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		return terminalResult.RowsAffected() + unenqueuedResult.RowsAffected(), fmt.Errorf("knowledge governance: iterate completed runs: %w", err)
	}

	replayed := int64(0)
	for _, taskID := range completedTaskIDs {
		task, err := r.Queries.GetAgentTask(ctx, taskID)
		if err != nil {
			return terminalResult.RowsAffected() + unenqueuedResult.RowsAffected() + replayed, fmt.Errorf("knowledge governance: read completed task: %w", err)
		}
		var payload protocol.TaskCompletedPayload
		taskErr := ""
		if err := json.Unmarshal(task.Result, &payload); err != nil {
			taskErr = "invalid persisted completion result"
		}
		stats, err := r.processTaskTerminal(ctx, task, payload.Output, taskErr, true)
		if err != nil {
			return terminalResult.RowsAffected() + unenqueuedResult.RowsAffected() + replayed, fmt.Errorf("knowledge governance: replay completed task: %w", err)
		}
		if stats != nil {
			replayed++
		}
	}
	return terminalResult.RowsAffected() + unenqueuedResult.RowsAffected() + replayed, nil
}

func (r *Runner) RunWorkspace(ctx context.Context, workspaceID, dirID, actorID, trigger string) (*RunStats, error) {
	// A governance run becomes durable once creation starts. Continue the bounded
	// setup if the initiating HTTP request disconnects so no running row is left
	// without a task merely because its caller went away.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: invalid workspace id: %w", err)
	}
	dir, err := util.ParseUUID(dirID)
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: invalid directory id: %w", err)
	}
	actor, err := util.ParseUUID(actorID)
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: invalid actor id: %w", err)
	}
	if r.Queries == nil || r.Enqueue == nil {
		return nil, errors.New("knowledge governance: runner is not wired")
	}

	tx, err := r.DB.Begin(runCtx)
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: begin run: %w", err)
	}
	defer tx.Rollback(runCtx) //nolint:errcheck
	qtx := r.Queries.WithTx(tx)

	// This is the create half of the workspace-delete fence. Hold the shared
	// workspace lock through the task link so DeleteWorkspace cannot sweep the
	// directory or run after this transaction has verified either one.
	var lockedWorkspace pgtype.UUID
	if err := tx.QueryRow(runCtx, `
SELECT id FROM workspace WHERE id = $1 FOR KEY SHARE`, ws).Scan(&lockedWorkspace); err != nil {
		return nil, fmt.Errorf("knowledge governance: lock workspace: %w", err)
	}

	var scope string
	if err := tx.QueryRow(runCtx, `
SELECT label FROM knowledge_dir
WHERE id = $1 AND workspace_id = $2 AND removed = FALSE`, dir, ws).Scan(&scope); err != nil {
		return nil, fmt.Errorf("knowledge governance: scope: %w", err)
	}
	var agentID, runtimeID pgtype.UUID
	err = tx.QueryRow(runCtx, `
SELECT a.id, a.runtime_id
FROM retrospective_config c
JOIN agent a ON a.id = c.agent_id
WHERE c.workspace_id = $1 AND c.enabled = TRUE
  AND a.workspace_id = $1 AND a.archived_at IS NULL AND a.runtime_id IS NOT NULL`, ws).Scan(&agentID, &runtimeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoExecutor
	}
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: execution agent: %w", err)
	}

	now := r.now()
	title := fmt.Sprintf("知识库整理 · %s · %s", scope, now.Local().Format("2006-01-02 15:04"))
	var runID pgtype.UUID
	err = tx.QueryRow(runCtx, `
WITH active_directory AS (
	SELECT id
	FROM knowledge_dir
	WHERE id = $2 AND workspace_id = $1 AND removed = FALSE
	FOR UPDATE
)
INSERT INTO knowledge_governance_run (workspace_id, dir_id, agent_id, title, prompt_version, trigger, created_by)
SELECT $1, $2, $3, $4, $5, $6, $7
WHERE EXISTS (SELECT 1 FROM active_directory)
RETURNING id`, ws, dir, agentID, title, PromptVersion, trigger, actor).Scan(&runID)
	if isUniqueViolation(err) {
		return nil, ErrScopeRunning
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("knowledge governance: scope is no longer active")
	}
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: create run: %w", err)
	}

	entries, err := r.entrySnapshot(runCtx, tx, ws, dir)
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: snapshot entries: %w", err)
	}
	tc, _ := json.Marshal(TaskContext{
		Kind: TaskKind, RunID: util.UUIDToString(runID), WorkspaceID: workspaceID,
		DirID: dirID, Scope: scope, PromptVersion: PromptVersion, Entries: entries,
	})
	// The queue context drives execution, while the run retains the exact same
	// snapshot as durable audit evidence after task retention or cleanup.
	if _, err := tx.Exec(runCtx, `
UPDATE knowledge_governance_run
SET detail = $1::jsonb
WHERE id = $2 AND workspace_id = $3 AND status = 'running'`, tc, runID, ws); err != nil {
		return nil, fmt.Errorf("knowledge governance: persist snapshot: %w", err)
	}
	task, err := r.Enqueue(runCtx, qtx, EnqueueParams{
		RunID: util.UUIDToString(runID), AgentID: util.UUIDToString(agentID), RuntimeID: util.UUIDToString(runtimeID),
		OriginatorID: actorID, Context: tc,
	})
	if err != nil {
		return nil, fmt.Errorf("knowledge governance: enqueue: %w", err)
	}
	if err := tx.Commit(runCtx); err != nil {
		return nil, fmt.Errorf("knowledge governance: commit run: %w", err)
	}
	if r.Notify != nil {
		r.Notify(runCtx, task)
	}
	return &RunStats{RunID: util.UUIDToString(runID), TaskID: util.UUIDToString(task.ID), Title: title, Status: "running"}, nil
}

func (r *Runner) entrySnapshot(ctx context.Context, q db.DBTX, workspaceID, dirID pgtype.UUID) ([]EntrySnapshot, error) {
	rows, err := q.Query(ctx, `
SELECT id, key, content, content_sha256, current_version, lifecycle_state
FROM knowledge_entry
WHERE workspace_id = $1 AND dir_id = $2
ORDER BY key`, workspaceID, dirID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []EntrySnapshot{}
	for rows.Next() {
		var id pgtype.UUID
		var entry EntrySnapshot
		if err := rows.Scan(&id, &entry.Key, &entry.Content, &entry.ContentSHA256, &entry.CurrentVersion, &entry.LifecycleState); err != nil {
			return nil, err
		}
		entry.ID = util.UUIDToString(id)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (r *Runner) ProcessTaskTerminal(ctx context.Context, task db.AgentTaskQueue, output, taskErr string) (*RunStats, error) {
	return r.processTaskTerminal(ctx, task, output, taskErr, false)
}

func (r *Runner) processTaskTerminal(ctx context.Context, task db.AgentTaskQueue, output, taskErr string, allowPersistenceReplay bool) (*RunStats, error) {
	tc, ok := ParseTaskContext(task.Context)
	if !ok {
		return nil, fmt.Errorf("knowledge governance: invalid task context")
	}
	ws := util.MustParseUUID(tc.WorkspaceID)
	runID := util.MustParseUUID(tc.RunID)
	var status, reportReason string
	if err := r.DB.QueryRow(ctx, `
	SELECT status, COALESCE(report->>'reason', '')
	FROM knowledge_governance_run WHERE id = $1 AND workspace_id = $2`, runID, ws).Scan(&status, &reportReason); err != nil {
		return nil, err
	}
	replayPending := task.Status == "completed" && status == "failed" && reportReason == "report_persistence_failed"
	if status != "running" && !(allowPersistenceReplay && replayPending) {
		return nil, nil
	}
	if task.Status == "cancelled" {
		return &RunStats{RunID: tc.RunID, Status: "cancelled"}, r.finish(ctx, runID, ws, "cancelled", map[string]any{"reason": "cancelled"})
	}
	if strings.TrimSpace(taskErr) != "" || strings.TrimSpace(output) == "" {
		return &RunStats{RunID: tc.RunID, Status: "failed"}, r.finish(ctx, runID, ws, "failed", map[string]any{"reason": "agent_failed"})
	}
	report, err := ParseRunOutput(output, tc)
	if err != nil {
		return &RunStats{RunID: tc.RunID, Status: "failed"}, r.finish(ctx, runID, ws, "failed", map[string]any{"reason": "invalid_report"})
	}
	persisted, err := r.persistReport(ctx, runID, ws, tc, report, allowPersistenceReplay)
	if err != nil {
		// Release the directory scope while retaining a durable replay marker.
		// ReconcileStaleRuns replays the immutable task.Result until it persists.
		finishErr := r.finish(ctx, runID, ws, "failed", map[string]any{"reason": "report_persistence_failed"})
		if finishErr != nil {
			return nil, fmt.Errorf("knowledge governance: persist report: %w; finish failed: %v", err, finishErr)
		}
		return &RunStats{RunID: tc.RunID, Status: "failed"}, fmt.Errorf("knowledge governance: persist report: %w", err)
	}
	if !persisted {
		return nil, nil
	}
	return &RunStats{RunID: tc.RunID, Status: "succeeded"}, nil
}

func ParseRunOutput(raw string, tc TaskContext) (RunOutput, error) {
	var out RunOutput
	if err := json.Unmarshal([]byte(stripJSONFences(raw)), &out); err != nil {
		return RunOutput{}, fmt.Errorf("knowledge governance: invalid JSON: %w", err)
	}
	if strings.TrimSpace(out.BackupReference) == "" {
		return RunOutput{}, errors.New("knowledge governance: backup reference is required")
	}
	entries := make(map[string]EntrySnapshot, len(tc.Entries))
	for _, entry := range tc.Entries {
		entries[entry.ID] = entry
	}
	if len(out.Findings) != len(entries) {
		return RunOutput{}, errors.New("knowledge governance: every snapshot entry needs one finding")
	}
	seen := map[string]bool{}
	valid := map[string]bool{}
	for _, finding := range out.Findings {
		entry, exists := entries[finding.EntryID]
		if !exists || seen[finding.EntryID] {
			return RunOutput{}, errors.New("knowledge governance: findings must map one-to-one to the snapshot")
		}
		seen[finding.EntryID] = true
		if finding.Key != entry.Key || finding.OriginalContent != entry.Content || finding.ContentVersion != entry.CurrentVersion || finding.ContentSHA256 != entry.ContentSHA256 || finding.Scope != tc.Scope {
			return RunOutput{}, fmt.Errorf("knowledge governance: finding %s does not match the immutable snapshot", finding.EntryID)
		}
		if !validVerdict(finding.Verdict) || !validProblemCategories(finding.ProblemCategories) || strings.TrimSpace(finding.ApplicableScope) == "" || strings.TrimSpace(finding.Recommendation) == "" || len(finding.CounterEvidence) == 0 {
			return RunOutput{}, fmt.Errorf("knowledge governance: finding %s lacks a supported conclusion", finding.EntryID)
		}
		if finding.WritebackAuthorized {
			return RunOutput{}, errors.New("knowledge governance: agent reports cannot authorize writeback")
		}
		for _, evidence := range finding.CounterEvidence {
			if err := validateEvidence(evidence); err != nil {
				return RunOutput{}, fmt.Errorf("knowledge governance: finding %s evidence: %w", finding.EntryID, err)
			}
		}
		if finding.Verdict == "valid" {
			valid[finding.EntryID] = true
		}
	}
	coverage := map[string]bool{}
	for _, item := range out.RetainedCoverage {
		if !valid[item.EntryID] || coverage[item.EntryID] {
			return RunOutput{}, errors.New("knowledge governance: retained coverage must match valid findings")
		}
		if err := validateEvidence(item.CoverageEvidence); err != nil {
			return RunOutput{}, fmt.Errorf("knowledge governance: retained coverage: %w", err)
		}
		if err := validateEvidence(item.SampledCounterevidence); err != nil {
			return RunOutput{}, fmt.Errorf("knowledge governance: retained counter-evidence: %w", err)
		}
		coverage[item.EntryID] = true
	}
	if len(coverage) != len(valid) {
		return RunOutput{}, errors.New("knowledge governance: every valid finding needs coverage and sampled counter-evidence")
	}
	for _, regression := range out.RetrievalRegressions {
		if _, exists := entries[regression.EntryID]; !exists || strings.TrimSpace(regression.Expected) == "" || (strings.TrimSpace(regression.Observed) == "" && strings.TrimSpace(regression.NotRunReason) == "") {
			return RunOutput{}, errors.New("knowledge governance: retrieval regressions require a scoped outcome or a not-run reason")
		}
	}
	return out, nil
}

func (r *Runner) persistReport(ctx context.Context, runID, workspaceID pgtype.UUID, tc TaskContext, report RunOutput, allowPersistenceReplay bool) (bool, error) {
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status, reportReason string
	if err := tx.QueryRow(ctx, `
	SELECT status, COALESCE(report->>'reason', '') FROM knowledge_governance_run
	WHERE id = $1 AND workspace_id = $2
	FOR UPDATE`, runID, workspaceID).Scan(&status, &reportReason); err != nil {
		return false, err
	}
	if status != "running" && !(allowPersistenceReplay && status == "failed" && reportReason == "report_persistence_failed") {
		return false, nil
	}
	counts := map[string]int{"scanned": len(report.Findings), "valid": 0, "revised": 0, "deprecated": 0, "superseded": 0, "duplicate": 0, "invalid": 0, "needs_review": 0}
	for _, finding := range report.Findings {
		switch finding.Verdict {
		case "valid":
			counts["valid"]++
		case "revised":
			counts["revised"]++
		case "deprecated":
			counts["deprecated"]++
		case "superseded":
			counts["superseded"]++
		case "duplicate":
			counts["duplicate"]++
		case "invalid":
			counts["invalid"]++
		case "needs_review":
			counts["needs_review"]++
		}
		categories, _ := json.Marshal(finding.ProblemCategories)
		evidence, _ := json.Marshal(finding.CounterEvidence)
		var replacement pgtype.UUID
		if strings.TrimSpace(finding.ReplacementEntryID) != "" {
			parsed, err := util.ParseUUID(finding.ReplacementEntryID)
			if err != nil {
				return false, fmt.Errorf("knowledge governance: replacement entry id: %w", err)
			}
			replacement = parsed
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO knowledge_governance_finding (
 run_id, workspace_id, entry_id, entry_key, original_content, content_version, content_sha256, scope,
 verdict, problem_categories, counter_evidence, applicable_scope, replacement_entry_id, recommendation,
 writeback_authorized, lifecycle_event_ref
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb,$12,$13,$14,FALSE,$15)`,
			runID, workspaceID, util.MustParseUUID(finding.EntryID), finding.Key, finding.OriginalContent,
			finding.ContentVersion, finding.ContentSHA256, finding.Scope, finding.Verdict, categories, evidence,
			finding.ApplicableScope, replacement, finding.Recommendation, finding.LifecycleEventRef); err != nil {
			return false, err
		}
	}
	payload := map[string]any{
		"prompt_version": PromptVersion, "counts": counts, "retained_coverage": report.RetainedCoverage,
		"retrieval_regressions": report.RetrievalRegressions, "backup_reference": report.BackupReference,
	}
	raw, _ := json.Marshal(payload)
	if _, err := tx.Exec(ctx, `
UPDATE knowledge_governance_run
SET status = 'succeeded', report = $1::jsonb, finished_at = now()
	WHERE id = $2 AND workspace_id = $3`, raw, runID, workspaceID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Runner) finish(ctx context.Context, runID, workspaceID pgtype.UUID, status string, report map[string]any) error {
	raw, _ := json.Marshal(report)
	_, err := r.DB.Exec(ctx, `
UPDATE knowledge_governance_run
SET status = $1, report = $2::jsonb, finished_at = now()
WHERE id = $3 AND workspace_id = $4 AND status = 'running'`, status, raw, runID, workspaceID)
	return err
}

func validVerdict(verdict string) bool {
	switch verdict {
	case "valid", "revised", "deprecated", "superseded", "duplicate", "invalid", "needs_review":
		return true
	default:
		return false
	}
}

func validProblemCategories(categories []string) bool {
	if len(categories) == 0 {
		return false
	}
	for _, category := range categories {
		if strings.TrimSpace(category) == "" {
			return false
		}
	}
	return true
}

func validateEvidence(e Evidence) error {
	switch e.SourceType {
	case "owner_correction", "issue", "pull_request", "code", "runtime_record":
	default:
		return errors.New("unknown source type")
	}
	if strings.TrimSpace(e.ImmutableRef) == "" || strings.TrimSpace(e.Excerpt) == "" {
		return errors.New("immutable reference and excerpt are required")
	}
	if _, err := time.Parse(time.RFC3339, e.ObservedAt); err != nil {
		return errors.New("observed_at must be RFC3339")
	}
	return nil
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
