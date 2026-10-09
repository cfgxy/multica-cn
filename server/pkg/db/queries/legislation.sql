-- Prompt legislation (RUYI-305): the proposal pool (E2), the per-carrier
-- structure baseline (E4) and the daily retrospective (E3). All tables are
-- workspace-keyed with no foreign keys (house rule); the handler validates
-- carrier_scope_id against the owning table on every write.

-- name: CreatePromptProposal :one
INSERT INTO prompt_proposal (
    workspace_id, carrier_scope, carrier_scope_id, target_section,
    change_kind, clause_name, clause_text,
    gate_answer_layer, gate_answer_retention, gate_answer_cost,
    gate_answer_conflict, gate_answer_dedup,
    evidence_anchors, status, merged_from, source,
    created_by_type, created_by_id, audit_log
) VALUES (
    @workspace_id, @carrier_scope, @carrier_scope_id, @target_section,
    @change_kind, @clause_name, @clause_text,
    @gate_answer_layer, @gate_answer_retention, @gate_answer_cost,
    @gate_answer_conflict, @gate_answer_dedup,
    @evidence_anchors, 'draft', '[]'::jsonb, @source,
    @created_by_type, @created_by_id, @audit_log::jsonb
) RETURNING *;

-- name: GetPromptProposal :one
SELECT * FROM prompt_proposal
WHERE prompt_proposal.id = $1 AND prompt_proposal.workspace_id = $2;

-- name: ListPromptProposals :many
-- Optional status filter: pass '' for the whole pool.
SELECT * FROM prompt_proposal
WHERE prompt_proposal.workspace_id = $1
  AND ($2 = '' OR prompt_proposal.status = $2)
ORDER BY prompt_proposal.created_at DESC
LIMIT 200;

-- name: UpdatePromptProposalDraft :one
-- Draft-only edit: the handler checks status = 'draft' in the same
-- transaction before calling this, so the WHERE clause carries the guard.
UPDATE prompt_proposal SET
    carrier_scope = @carrier_scope, carrier_scope_id = @carrier_scope_id, target_section = @target_section,
    change_kind = @change_kind, clause_name = @clause_name, clause_text = @clause_text,
    gate_answer_layer = @gate_answer_layer, gate_answer_retention = @gate_answer_retention, gate_answer_cost = @gate_answer_cost,
    gate_answer_conflict = @gate_answer_conflict, gate_answer_dedup = @gate_answer_dedup,
    evidence_anchors = @evidence_anchors::jsonb,
    updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'draft'
RETURNING *;

-- name: SubmitPromptProposal :one
-- jev_advisory (RUYI-347): the submit-time risk precheck report; NULL when
-- the advisory layer is disabled (NULL = 未检/关态).
UPDATE prompt_proposal SET status = 'pending_owner',
    audit_log = prompt_proposal.audit_log || @audit::jsonb,
    jev_advisory = @jev_advisory::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'draft'
RETURNING *;

-- name: ApprovePromptProposal :one
-- pending_owner → approved. The caller runs the gate and either enacts or
-- gate-fails the row inside the same transaction; this transition alone
-- exists so a crash after approval never loses the owner's decision.
UPDATE prompt_proposal SET status = 'approved',
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'pending_owner'
RETURNING *;

-- name: EnactPromptProposal :one
-- approved → enacted. enacted_version stays NULL in E1–E4 (the E5 write
-- path fills it); gate_errors/gate_warnings are cleared by a successful run.
-- jev_advisory (RUYI-347): the gate-stage soft-judgment report overwrites
-- the submit-stage precheck (latest verdict is authoritative; stage field
-- disambiguates).
UPDATE prompt_proposal SET status = 'enacted',
    gate_errors = '[]'::jsonb, gate_warnings = @warnings::jsonb,
    audit_log = prompt_proposal.audit_log || @audit::jsonb,
    jev_advisory = @jev_advisory::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'approved'
RETURNING *;

-- name: MarkPromptProposalGateFailed :one
-- approved → gate_failed with the full engine report stored for the UI.
-- jev_advisory (RUYI-347): gate-stage report, same overwrite rule as enact.
UPDATE prompt_proposal SET status = 'gate_failed',
    gate_errors = @errors::jsonb, gate_warnings = @warnings::jsonb,
    audit_log = prompt_proposal.audit_log || @audit::jsonb,
    jev_advisory = @jev_advisory::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status IN ('approved', 'pending_owner')
RETURNING *;

-- name: ReworkPromptProposal :one
-- gate_failed → draft (creator fixes the clause text or the five answers).
UPDATE prompt_proposal SET status = 'draft',
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'gate_failed'
RETURNING *;

-- name: RejectPromptProposal :one
UPDATE prompt_proposal SET status = 'rejected',
    rollback_reason = @reason, gate_errors = '[]'::jsonb, gate_warnings = '[]'::jsonb,
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'pending_owner'
RETURNING *;

-- name: RestorePromptProposal :one
-- rejected → draft (owner reopens a rejected proposal for revision).
UPDATE prompt_proposal SET status = 'draft',
    rollback_reason = '',
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'rejected'
RETURNING *;

-- name: AppendPromptProposalAudit :one
-- Out-of-band audit appends on rows that are not leaving their current
-- status: the carrier revert hook's rolled_back marker, merge bookkeeping.
UPDATE prompt_proposal SET
    audit_log = prompt_proposal.audit_log || @audit::jsonb,
    rollback_reason = CASE WHEN sqlc.narg('reason')::text IS NULL THEN prompt_proposal.rollback_reason ELSE sqlc.narg('reason')::text END,
    updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
RETURNING *;

-- name: MergePromptProposalEvidence :one
-- Pool dedup (E2): an intake candidate matching a live draft merges into it
-- instead of creating a row — evidence anchors accumulate, the candidate is
-- recorded in merged_from.
UPDATE prompt_proposal SET
    evidence_anchors = prompt_proposal.evidence_anchors || @anchors::jsonb,
    merged_from = prompt_proposal.merged_from || @merged_from::jsonb,
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id
RETURNING *;

-- name: FindMergablePromptProposal :one
-- Same-topic live match for intake dedup: same carrier, same section, same
-- change kind, same clause name, still in draft or pending_owner.
SELECT * FROM prompt_proposal
WHERE prompt_proposal.workspace_id = $1
  AND prompt_proposal.carrier_scope = $2
  AND prompt_proposal.carrier_scope_id = $3
  AND prompt_proposal.change_kind = $4
  AND prompt_proposal.clause_name = $5
  AND prompt_proposal.status IN ('draft', 'pending_owner')
ORDER BY prompt_proposal.created_at DESC
LIMIT 1;

-- name: AppendPromptProposalRollbackAudit :many
-- The carrier revert hook (prompt_version source='revert') marks every
-- enacted proposal of that carrier as rolled back — audit only; the row
-- stays enacted.
UPDATE prompt_proposal SET
    audit_log = prompt_proposal.audit_log || @audit::jsonb,
    rollback_reason = @reason, updated_at = now()
WHERE prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.carrier_scope = @carrier_scope
  AND prompt_proposal.carrier_scope_id = @carrier_scope_id
  AND prompt_proposal.status = 'enacted'
RETURNING id;

-- --- Structure baseline (E4) ---

-- name: GetWorkspacePromptContent :one
-- Read-only effective content of the workspace carrier (no row lock): the
-- legislation preview and the retrospective's duplicate pre-check read the
-- live clause set against it. Write paths must use the Lock*ForPromptVersion
-- family instead.
SELECT COALESCE(context, '')::text AS effective_content FROM workspace WHERE id = $1;

-- name: GetProjectPromptContent :one
SELECT COALESCE(instructions, '')::text AS effective_content FROM project
WHERE project.id = $1 AND project.workspace_id = $2;

-- name: GetSquadPromptContent :one
SELECT COALESCE(instructions, '')::text AS effective_content FROM squad
WHERE squad.id = $1 AND squad.workspace_id = $2;

-- name: GetAgentPromptContent :one
SELECT COALESCE(instructions, '')::text AS effective_content FROM agent
WHERE agent.id = $1 AND agent.workspace_id = $2;

-- name: GetPromptStructureBaseline :one
SELECT * FROM prompt_structure_baseline
WHERE prompt_structure_baseline.carrier_scope = $1
  AND prompt_structure_baseline.carrier_scope_id = $2;

-- name: UpsertPromptStructureBaseline :one
INSERT INTO prompt_structure_baseline (
    workspace_id, carrier_scope, carrier_scope_id, sections, clauses, content_sha256, updated_at
) VALUES (@workspace_id, @carrier_scope, @carrier_scope_id, @sections::jsonb, @clauses::jsonb, @content_sha256, now())
ON CONFLICT (carrier_scope, carrier_scope_id) DO UPDATE SET
    sections = EXCLUDED.sections,
    clauses = EXCLUDED.clauses,
    content_sha256 = EXCLUDED.content_sha256,
    updated_at = now()
RETURNING *;

-- --- Retrospective (E3) ---

-- name: GetRetrospectiveConfig :one
SELECT * FROM retrospective_config WHERE retrospective_config.workspace_id = @workspace_id;

-- name: UpsertRetrospectiveConfig :one
-- Full-row upsert: the handler reads current, merges the patch (agent
-- selection included, RUYI-552 direction 3) and writes everything back in
-- one statement. agent_id/updated_by are nullable: a disabled config may
-- clear the agent, and rows saved before migration 935 carry no saver.
INSERT INTO retrospective_config (workspace_id, enabled, include_in_review, window_days, agent_id, updated_by, updated_at)
VALUES (@workspace_id, @enabled, @include_in_review, @window_days, @agent_id, @updated_by, now())
ON CONFLICT (workspace_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    include_in_review = EXCLUDED.include_in_review,
    window_days = EXCLUDED.window_days,
    agent_id = EXCLUDED.agent_id,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: ListEnabledRetrospectiveConfigs :many
SELECT * FROM retrospective_config WHERE retrospective_config.enabled = true;

-- name: InsertRetrospectiveRun :one
-- detail carries the scanned issue ids from the start: the completion
-- processor validates the agent's reported issue ids against it, so the
-- run row is the single source of what this pass put in scope.
INSERT INTO retrospective_run (
    workspace_id, status, trigger, window_start, window_end, detail
) VALUES (@workspace_id, 'running', @trigger, @window_start, @window_end, @detail::jsonb)
RETURNING *;

-- name: SetRetrospectiveRunTaskID :exec
UPDATE retrospective_run SET task_id = @task_id WHERE id = @id;

-- name: UpdateRetrospectiveRunDetail :exec
UPDATE retrospective_run SET detail = @detail::jsonb WHERE id = @id;

-- name: GetRetrospectiveRunByTaskID :one
SELECT * FROM retrospective_run WHERE retrospective_run.task_id = @task_id;

-- name: FinishRetrospectiveRun :one
UPDATE retrospective_run SET
    status = @status, issues_scanned = @issues_scanned, issues_analyzed = @issues_analyzed,
    proposals_created = @proposals_created, proposals_merged = @proposals_merged, duplicates_skipped = @duplicates_skipped,
    error = @error, detail = @detail::jsonb, finished_at = now()
WHERE retrospective_run.id = @id AND retrospective_run.workspace_id = @workspace_id
RETURNING *;

-- name: ListRetrospectiveRuns :many
SELECT * FROM retrospective_run
WHERE retrospective_run.workspace_id = @workspace_id
ORDER BY retrospective_run.created_at DESC
LIMIT 50;

-- name: ReconcileTerminalRetrospectiveRuns :execrows
-- Bulk backstop for runs whose platform task went terminal without the
-- completion hook seeing it: offline-runtime sweeps, cancel paths and
-- daemon crashes all bypass FailTask. First terminal verdict wins — a run
-- the completion processor already finished (succeeded/failed) is skipped
-- by the status guard.
UPDATE retrospective_run r
SET status = 'failed',
    error = '关联的智能体运行未正常完成（任务失败、被取消或已过期）',
    finished_at = now()
FROM agent_task_queue t
WHERE r.task_id = t.id
  AND r.status = 'running'
  AND t.status IN ('failed', 'cancelled');

-- name: ReconcileUnenqueuedRetrospectiveRuns :execrows
-- Runs whose task never made it into the queue (enqueue interrupted, or the
-- teardown fence refused) would stay "running" forever; age them out.
UPDATE retrospective_run
SET status = 'failed',
    error = '复盘任务未能入列（工作空间或智能体已不可用），本轮已终止',
    finished_at = now()
WHERE retrospective_run.status = 'running'
  AND retrospective_run.task_id IS NULL
  AND retrospective_run.created_at < now() - interval '10 minutes';

-- name: HasRetrospectiveWatermark :one
-- Exists-shaped probe: 1 when the issue was already analyzed by any run.
-- count(*) keeps :one always returning a row — a fresh issue has no
-- watermark, and ErrNoRows there must not fail the whole run.
SELECT count(*) FROM retrospective_issue_watermark
WHERE retrospective_issue_watermark.workspace_id = @workspace_id
  AND retrospective_issue_watermark.issue_id = @issue_id;

-- name: InsertRetrospectiveWatermark :exec
INSERT INTO retrospective_issue_watermark (workspace_id, issue_id, last_run_id)
VALUES (@workspace_id, @issue_id, @last_run_id)
ON CONFLICT DO NOTHING;

-- name: CreateRetrospectiveTask :one
-- The daily retrospective's one agent run (RUYI-552 direction 3), enqueued
-- on the existing no-issue path. issue_id is NULL by construction — the run
-- must never create an issue or comment — and originator_source='retrospective'
-- keeps it out of the quick_create production statistic, the same discipline
-- as the prompt-quiz run.
--
-- originator_user_id/accountable_user_id carry the member who last saved the
-- config (the honest human originator); NULL for configs saved before
-- migration 935.
--
-- Fenced against workspace teardown by lock_task_owner_rows (migration 284),
-- exactly like the quiz enqueue: the owners' workspace rows are locked in
-- this statement's own transaction and the INSERT writes no row once they
-- are gone. Returning no row is that refusal, not an error.
INSERT INTO agent_task_queue (
    agent_id,
    runtime_id,
    issue_id,
    status,
    priority,
    context,
    originator_user_id,
    accountable_user_id,
    originator_source,
    trigger_evidence_kind,
    trigger_evidence_ref_id
)
SELECT
    sqlc.arg('agent_id'), sqlc.arg('runtime_id'), NULL::uuid, 'queued', sqlc.arg('priority'),
    sqlc.arg('context'),
    sqlc.narg('originator_user_id'), sqlc.narg('accountable_user_id'),
    'retrospective', 'retrospective_run', sqlc.arg('run_id')
WHERE lock_task_owner_rows(sqlc.arg('agent_id'), NULL::uuid, sqlc.arg('runtime_id'))
RETURNING *;

-- name: ListIssuesCompletedInWindow :many
-- 已完成口径: done always; in_review only when the config widens the scan.
-- The issue table has no completed_at — updated_at under a terminal-ish
-- status is the best available completion signal, and the per-issue
-- watermark makes overlap harmless.
SELECT id, title, description, status, acceptance_criteria, updated_at
FROM issue
WHERE issue.workspace_id = $1
  AND issue.status = $2
  AND issue.updated_at >= $3 AND issue.updated_at < $4
ORDER BY issue.updated_at ASC
LIMIT 50;

-- name: ListIssueCommentsForRetrospective :many
-- The issue's real execution content: discussion and progress updates only —
-- never status_change/system bookkeeping rows, and never the retrospective's
-- own past output (it writes nothing to issues by design).
SELECT comment.author_type, comment.author_id, comment.content, comment.created_at
FROM comment
WHERE comment.issue_id = $1
  AND comment.type IN ('comment', 'progress_update')
ORDER BY comment.created_at ASC
LIMIT 100;
