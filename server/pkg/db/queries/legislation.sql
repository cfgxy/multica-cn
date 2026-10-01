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
UPDATE prompt_proposal SET status = 'pending_owner',
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
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
UPDATE prompt_proposal SET status = 'enacted',
    gate_errors = '[]'::jsonb, gate_warnings = @warnings::jsonb,
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
WHERE prompt_proposal.id = @id AND prompt_proposal.workspace_id = @workspace_id
  AND prompt_proposal.status = 'approved'
RETURNING *;

-- name: MarkPromptProposalGateFailed :one
-- approved → gate_failed with the full engine report stored for the UI.
UPDATE prompt_proposal SET status = 'gate_failed',
    gate_errors = @errors::jsonb, gate_warnings = @warnings::jsonb,
    audit_log = prompt_proposal.audit_log || @audit::jsonb, updated_at = now()
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
INSERT INTO retrospective_config (workspace_id, enabled, include_in_review, window_days, updated_at)
VALUES (@workspace_id, @enabled, @include_in_review, @window_days, now())
ON CONFLICT (workspace_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    include_in_review = EXCLUDED.include_in_review,
    window_days = EXCLUDED.window_days,
    updated_at = now()
RETURNING *;

-- name: ListEnabledRetrospectiveConfigs :many
SELECT * FROM retrospective_config WHERE retrospective_config.enabled = true;

-- name: InsertRetrospectiveRun :one
INSERT INTO retrospective_run (
    workspace_id, status, trigger, window_start, window_end
) VALUES (@workspace_id, 'running', @trigger, @window_start, @window_end)
RETURNING *;

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
