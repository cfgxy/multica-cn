-- Clause assets (RUYI-751 / ADR 007 phase 1): identity, versions, bindings,
-- injection events, daily quality. All workspace-scoped with no foreign keys
-- (house rule); the handler validates entity references on every write and
-- DeleteWorkspace* owns the sweep.

-- name: CreateClause :one
INSERT INTO clause (workspace_id, slug, title, status, created_by_type, created_by_id)
VALUES (@workspace_id, @slug, @title, 'active', @created_by_type, @created_by_id)
RETURNING *;

-- name: GetClauseByID :one
SELECT * FROM clause WHERE clause.id = $1 AND clause.workspace_id = $2;

-- name: GetClauseBySlug :one
SELECT * FROM clause WHERE clause.slug = $1 AND clause.workspace_id = $2;

-- name: UpdateClauseStatus :one
UPDATE clause SET status = @status WHERE clause.id = @id AND clause.workspace_id = @workspace_id
RETURNING *;

-- name: ListClausesForWorkspace :many
SELECT * FROM clause WHERE clause.workspace_id = $1 ORDER BY clause.created_at, clause.slug;

-- name: CreateClauseVersion :one
INSERT INTO clause_version (
    clause_id, version, content, content_sha256, source,
    carrier_scope, carrier_version, created_by_type, created_by_id
) VALUES (
    @clause_id, @version, @content, @content_sha256, @source,
    @carrier_scope, @carrier_version, @created_by_type, @created_by_id
)
RETURNING *;

-- name: LatestClauseVersion :one
SELECT version FROM clause_version
WHERE clause_version.clause_id = $1
ORDER BY clause_version.version DESC
LIMIT 1;

-- name: GetClauseVersion :one
SELECT * FROM clause_version
WHERE clause_version.clause_id = $1 AND clause_version.version = $2;

-- name: ListClauseVersions :many
SELECT * FROM clause_version
WHERE clause_version.clause_id = $1
ORDER BY clause_version.version;

-- name: CreateClauseBinding :one
INSERT INTO clause_binding (
    clause_id, scope, scope_id, status, conditions, since_clause_version, audit_log
) VALUES (
    @clause_id, @scope, @scope_id, 'active', @conditions, @since_clause_version, @audit_log::jsonb
)
RETURNING *;

-- name: GetActiveClauseBinding :one
SELECT * FROM clause_binding
WHERE clause_binding.clause_id = $1 AND clause_binding.status = 'active';

-- name: SupersedeClauseBinding :one
UPDATE clause_binding SET status = 'superseded',
    audit_log = clause_binding.audit_log || @audit::jsonb, updated_at = now()
WHERE clause_binding.clause_id = @clause_id AND clause_binding.status = 'active'
RETURNING *;

-- name: WithdrawClauseBinding :one
UPDATE clause_binding SET status = 'withdrawn',
    audit_log = clause_binding.audit_log || @audit::jsonb, updated_at = now()
WHERE clause_binding.clause_id = @clause_id AND clause_binding.status = 'active'
RETURNING *;

-- name: ListClauseBindings :many
SELECT * FROM clause_binding
WHERE clause_binding.clause_id = $1
ORDER BY clause_binding.created_at DESC, clause_binding.id;

-- ── Effective Clause Set (claim hot path) ─────────────────────────────────
-- The point query per carrier: active bindings of this carrier entity whose
-- conditions admit the task kind (NULL conditions / missing task_kinds key =
-- unconditional), joined to the clause and its newest version. Deterministic
-- order: slug. Same input → same rows, same order (ADR 007 §5).

-- name: ListEffectiveCarrierClauses :many
SELECT c.id AS clause_id, c.slug, c.title, c.status AS clause_status,
       cb.conditions, cb.since_clause_version,
       cv.version, cv.content, cv.content_sha256
FROM clause_binding cb
JOIN clause c ON c.id = cb.clause_id AND c.status = 'active'
JOIN LATERAL (
    SELECT v.version, v.content, v.content_sha256
    FROM clause_version v
    WHERE v.clause_id = cb.clause_id
    ORDER BY v.version DESC
    LIMIT 1
) cv ON TRUE
WHERE cb.scope = sqlc.arg('scope')::text
  AND cb.scope_id = sqlc.arg('scope_id')::uuid
  AND cb.status = 'active'
  AND (
    cb.conditions IS NULL
    OR cb.conditions->'task_kinds' IS NULL
    OR jsonb_typeof(cb.conditions->'task_kinds') <> 'array'
    OR sqlc.arg('task_kind')::text IN (SELECT jsonb_array_elements_text(cb.conditions->'task_kinds'))
  )
ORDER BY c.slug;

-- name: ListActiveWorkspaceClauses :many
-- The snapshot re-render set: every active binding in the workspace, no
-- condition filtering (the snapshot is the full task-kind union per ADR
-- 007 §业务列渲染快照). Ordered scope_rank → slug so each carrier's region is
-- byte-stable.
SELECT c.id AS clause_id, c.slug, c.title, cv.version, cv.content,
       cb.scope, cb.scope_id,
       CASE cb.scope
           WHEN 'workspace' THEN 0
           WHEN 'squad' THEN 1
           WHEN 'project' THEN 2
           ELSE 3
       END AS scope_rank
FROM clause_binding cb
JOIN clause c ON c.id = cb.clause_id AND c.status = 'active'
JOIN LATERAL (
    SELECT v.version, v.content
    FROM clause_version v
    WHERE v.clause_id = cb.clause_id
    ORDER BY v.version DESC
    LIMIT 1
) cv ON TRUE
WHERE cb.status = 'active'
  AND c.workspace_id = sqlc.arg('workspace_id')::uuid
ORDER BY scope_rank, c.slug;

-- ── Injection events ───────────────────────────────────────────────────────

-- name: InsertClauseInjection :exec
-- Idempotent by the (task_id, clause_id, clause_version) unique index: a
-- re-claimed task re-writes the same rows harmlessly (prompt_quiz_result
-- precedent).
INSERT INTO clause_injection (
    workspace_id, task_id, clause_id, clause_version, carrier_scope, carrier_version, degraded
) VALUES (
    @workspace_id, @task_id, @clause_id, @clause_version, @carrier_scope, @carrier_version, @degraded
)
ON CONFLICT (task_id, clause_id, clause_version) DO NOTHING;

-- name: CountClauseInjections :one
-- Injection calls + covered runs over a trailing window, for the governance
-- list. Excluded: nothing — every row is an injection.
SELECT COUNT(*)::bigint AS injections,
       COUNT(DISTINCT task_id)::bigint AS runs
FROM clause_injection
WHERE clause_injection.clause_id = sqlc.arg('clause_id')::uuid
  AND clause_injection.created_at > sqlc.arg('since')::timestamptz;

-- name: SumClauseInjectionTokens :one
-- D1 for one clause over the window: injected clause tokens carried by the
-- daily rollup rows (NULL = not yet rolled up, not zero).
SELECT COALESCE(SUM(injected_tokens), 0)::bigint AS total_tokens
FROM clause_quality_daily
WHERE clause_quality_daily.clause_id = sqlc.arg('clause_id')::uuid
  AND clause_quality_daily.day >= sqlc.arg('since_day')::date;

-- ── Quality rollup (clause_quality_daily) ─────────────────────────────────

-- name: GetClauseQualityWatermark :one
SELECT watermark_at, last_run_started_at, last_run_finished_at, last_run_rows, last_error
FROM clause_quality_rollup_state WHERE id = 1;

-- name: MarkClauseQualityRunStarted :exec
UPDATE clause_quality_rollup_state
SET last_run_started_at = now(), last_error = NULL WHERE id = 1;

-- name: MarkClauseQualityRunFinished :exec
UPDATE clause_quality_rollup_state
SET last_run_finished_at = now(), last_run_rows = sqlc.arg('rows_affected')::bigint,
    last_error = NULL WHERE id = 1;

-- name: MarkClauseQualityRunFailed :exec
UPDATE clause_quality_rollup_state
SET last_error = sqlc.arg('last_error') WHERE id = 1;

-- name: AdvanceClauseQualityWatermark :exec
UPDATE clause_quality_rollup_state
SET watermark_at = GREATEST(watermark_at, sqlc.arg('watermark')::timestamptz),
    last_run_finished_at = now(), last_run_rows = sqlc.arg('rows_affected')::bigint,
    last_error = NULL WHERE id = 1;

-- name: ListClauseQualityDirtyBuckets :many
-- Buckets any injection event finished a run into since the watermark, with
-- the run set re-derived per bucket at recompute time. The same
-- "list dirty, recompute whole bucket" contract as the prompt rollup.
SELECT ci.workspace_id, ci.clause_id, ci.clause_version,
       (atq.completed_at AT TIME ZONE 'UTC')::date AS day,
       MAX(atq.completed_at) AS max_completed_at
FROM clause_injection ci
JOIN agent_task_queue atq ON atq.id = ci.task_id
WHERE atq.status IN ('completed', 'failed', 'cancelled')
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at > sqlc.arg('watermark')::timestamptz
GROUP BY ci.workspace_id, ci.clause_id, ci.clause_version, day
ORDER BY MAX(atq.completed_at)
LIMIT sqlc.arg('row_limit')::int;

-- name: ListClauseQualityBucketRuns :many
-- Every run in one (clause, version, day) bucket with the per-run facts the
-- D1/D5/D6 folds need — the clause_injection analogue of
-- ListPromptQualityBucketRuns. One row per run: clause_injection's unique
-- index guarantees a task can only meet one bucket row per (clause,
-- version), and the bucket key pins the version.
SELECT
    atq.id AS task_id,
    atq.issue_id,
    atq.status,
    COALESCE(NULLIF(atq.failure_reason, ''), '')::text AS failure_reason,
    atq.attempt,
    (tu.total_tokens IS NOT NULL)::boolean AS usage_measured,
    COALESCE(tu.total_tokens, 0)::bigint AS run_tokens
FROM clause_injection ci
JOIN agent_task_queue atq ON atq.id = ci.task_id
LEFT JOIN LATERAL (
    SELECT SUM(u.input_tokens + u.output_tokens)::bigint AS total_tokens
    FROM task_usage u
    WHERE u.task_id = atq.id
) tu ON TRUE
WHERE ci.clause_id = sqlc.arg('clause_id')::uuid
  AND ci.clause_version = sqlc.arg('clause_version')::int
  AND atq.status IN ('completed', 'failed', 'cancelled')
  AND atq.completed_at IS NOT NULL
  AND (atq.completed_at AT TIME ZONE 'UTC')::date = sqlc.arg('day')::date;

-- name: UpsertClauseQualityDaily :exec
INSERT INTO clause_quality_daily (
    workspace_id, clause_id, clause_version, day,
    finished_runs,
    injected_tokens, run_tokens_median,
    discipline_score_median, discipline_covered_runs, discipline_deductions,
    tool_results_measured, tool_results_error,
    attempt_total, retried_runs,
    attributable_failed_runs, excluded_failed_runs, failure_reason_counts,
    first_pass_issues, reviewed_issues,
    updated_at
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('clause_id'), sqlc.arg('clause_version'), sqlc.arg('day'),
    sqlc.arg('finished_runs'),
    sqlc.narg('injected_tokens'), sqlc.narg('run_tokens_median'),
    sqlc.narg('discipline_score_median'), sqlc.arg('discipline_covered_runs'), sqlc.arg('discipline_deductions'),
    sqlc.arg('tool_results_measured'), sqlc.arg('tool_results_error'),
    sqlc.arg('attempt_total'), sqlc.arg('retried_runs'),
    sqlc.arg('attributable_failed_runs'), sqlc.arg('excluded_failed_runs'), sqlc.arg('failure_reason_counts'),
    sqlc.arg('first_pass_issues'), sqlc.arg('reviewed_issues'),
    now()
)
ON CONFLICT (clause_id, clause_version, day) DO UPDATE SET
    workspace_id = EXCLUDED.workspace_id,
    finished_runs = EXCLUDED.finished_runs,
    injected_tokens = EXCLUDED.injected_tokens,
    run_tokens_median = EXCLUDED.run_tokens_median,
    discipline_score_median = EXCLUDED.discipline_score_median,
    discipline_covered_runs = EXCLUDED.discipline_covered_runs,
    discipline_deductions = EXCLUDED.discipline_deductions,
    tool_results_measured = EXCLUDED.tool_results_measured,
    tool_results_error = EXCLUDED.tool_results_error,
    attempt_total = EXCLUDED.attempt_total,
    retried_runs = EXCLUDED.retried_runs,
    attributable_failed_runs = EXCLUDED.attributable_failed_runs,
    excluded_failed_runs = EXCLUDED.excluded_failed_runs,
    failure_reason_counts = EXCLUDED.failure_reason_counts,
    first_pass_issues = EXCLUDED.first_pass_issues,
    reviewed_issues = EXCLUDED.reviewed_issues,
    updated_at = now();

-- name: ListClauseQualityDaily :many
SELECT * FROM clause_quality_daily
WHERE clause_quality_daily.clause_id = sqlc.arg('clause_id')::uuid
  AND clause_quality_daily.day >= sqlc.arg('since_day')::date
ORDER BY clause_quality_daily.day DESC, clause_quality_daily.clause_version DESC
LIMIT 400;

-- name: SumClauseVersionWindow :one
-- Whole-window aggregates for one (clause, version) — the rollback-suggestion
-- statistics input (ADR 007 §7): prompt-attributable failure rate over the
-- version's full recorded history.
SELECT COALESCE(SUM(finished_runs), 0)::bigint AS finished_runs,
       COALESCE(SUM(attributable_failed_runs), 0)::bigint AS attributable_failed_runs,
       COALESCE(SUM(excluded_failed_runs), 0)::bigint AS excluded_failed_runs
FROM clause_quality_daily
WHERE clause_quality_daily.clause_id = sqlc.arg('clause_id')::uuid
  AND clause_quality_daily.clause_version = sqlc.arg('clause_version')::int;

-- ── Governance / reconciliation reads ─────────────────────────────────────

-- name: CountClausesByStatus :many
SELECT status, COUNT(*)::bigint AS n FROM clause
WHERE clause.workspace_id = sqlc.arg('workspace_id')::uuid
GROUP BY status;

-- name: CountDegradedInjections :one
-- Degraded runs in the trailing window: injection rows recorded from the
-- snapshot fallback. Distinct task count — a degraded claim writes one flag
-- per row, and the run is the unit that matters.
SELECT COUNT(DISTINCT task_id)::bigint AS degraded_runs
FROM clause_injection
WHERE clause_injection.workspace_id = sqlc.arg('workspace_id')::uuid
  AND clause_injection.degraded
  AND clause_injection.created_at > sqlc.arg('since')::timestamptz;

-- name: ListClauseProposals :many
-- Proposal provenance for a clause detail page.
SELECT * FROM prompt_proposal
WHERE prompt_proposal.clause_id = sqlc.arg('clause_id')::uuid
ORDER BY prompt_proposal.created_at DESC
LIMIT 50;

-- name: ListClausesByTitle :many
-- Legacy name-fallback resolution: proposals predating the clause_id
-- reference key carry only the display title. Zero rows = unknown clause;
-- more than one = ambiguous, the caller must fail closed.
SELECT * FROM clause
WHERE clause.workspace_id = sqlc.arg('workspace_id')::uuid
  AND clause.title = sqlc.arg('title')::text
ORDER BY clause.created_at;

-- name: FindLiveRollbackSuggestion :one
-- Suppression check for auto-generated rollback drafts: a live (not
-- rejected/gate-failed) suggestion for the same clause and version already
-- exists.
SELECT COUNT(*)::int AS n FROM prompt_proposal
WHERE prompt_proposal.clause_id = sqlc.arg('clause_id')::uuid
  AND prompt_proposal.rollback_of_version = sqlc.arg('rollback_of_version')::int
  AND prompt_proposal.status IN ('draft', 'pending_owner', 'approved', 'enacted');

-- name: ListCarrierBusinessColumns :many
-- Reconciliation input: every agent-tier business column in the workspace.
-- The workspace/project/squad tiers are single/few rows; the agent tier can
-- be large, hence the caller-side LIMIT.
SELECT id AS scope_id, instructions AS business_column, 'agent'::text AS scope
FROM agent
WHERE agent.workspace_id = sqlc.arg('workspace_id')::uuid
LIMIT sqlc.arg('row_limit')::int;

-- name: ListWorkspaceContextRow :many
SELECT id AS scope_id, COALESCE(context, '') AS business_column, 'workspace'::text AS scope
FROM workspace WHERE workspace.id = sqlc.arg('workspace_id')::uuid;

-- name: ListProjectBusinessColumns :many
SELECT id AS scope_id, instructions AS business_column, 'project'::text AS scope
FROM project
WHERE project.workspace_id = sqlc.arg('workspace_id')::uuid
LIMIT sqlc.arg('row_limit')::int;

-- name: ListSquadBusinessColumns :many
SELECT id AS scope_id, instructions AS business_column, 'squad'::text AS scope
FROM squad
WHERE squad.workspace_id = sqlc.arg('workspace_id')::uuid
LIMIT sqlc.arg('row_limit')::int;
