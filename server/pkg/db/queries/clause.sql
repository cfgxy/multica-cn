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

-- name: GetClauseByIDDirect :one
-- Unscoped by workspace: the rollup and rollback checker walk candidates by
-- id (UUIDs are globally unique); handlers keep the scoped variant.
SELECT * FROM clause WHERE clause.id = $1;

-- name: UpdateClauseStatus :one
UPDATE clause SET status = @status WHERE clause.id = @id AND clause.workspace_id = @workspace_id
RETURNING *;

-- name: LockClauseForDirectManagement :one
SELECT * FROM clause
WHERE clause.id = @id AND clause.workspace_id = @workspace_id
FOR UPDATE;

-- name: DeleteClauseBindings :exec
DELETE FROM clause_binding WHERE clause_id = $1;

-- name: DeleteClauseVersions :exec
DELETE FROM clause_version WHERE clause_id = $1;

-- name: DeleteClauseQuality :exec
DELETE FROM clause_quality_daily WHERE clause_id = $1;

-- name: DeleteClauseInjections :exec
DELETE FROM clause_injection WHERE clause_id = $1;

-- name: DeleteClause :execrows
DELETE FROM clause WHERE id = $1 AND workspace_id = $2;

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

-- name: BumpClauseBindingVersion :one
-- revise_clause: the active binding tracks the newest enacted version and may
-- pick up new conditions in the same proposal (ADR 007 §2 — the two state
-- lines may ride one proposal).
UPDATE clause_binding SET
    since_clause_version = @since_clause_version,
    conditions = @conditions,
    audit_log = clause_binding.audit_log || @audit::jsonb,
    updated_at = now()
WHERE clause_binding.id = @id
RETURNING *;

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

-- name: InsertClauseInjectionObservation :exec
-- One durable row per degraded carrier claim, including snapshots that cannot
-- be parsed and therefore produce no clause_injection rows.
INSERT INTO clause_injection_observation (
    workspace_id, task_id, carrier_scope, carrier_scope_id, degraded, degradation_reason
) VALUES (
    @workspace_id, @task_id, @carrier_scope, @carrier_scope_id, @degraded, @degradation_reason
);

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
       MAX(atq.completed_at)::timestamptz AS max_completed_at
FROM clause_injection ci
JOIN agent_task_queue atq ON atq.id = ci.task_id
WHERE atq.status IN ('completed', 'failed', 'cancelled')
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at > sqlc.arg('watermark')::timestamptz
GROUP BY ci.workspace_id, ci.clause_id, ci.clause_version, day
ORDER BY MAX(atq.completed_at)::timestamptz
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

-- name: HasRollbackSuggestionForVersion :one
-- Planned-comparison latch (ADR 007 §7: 每版本一次计划内对比): ANY prior
-- suggestion for the same (clause, version) — regardless of status — means
-- the planned comparison already ran and must not repeat. An auto-generated
-- draft the Owner rejected must never come back on a later tick.
SELECT COUNT(*)::int AS n FROM prompt_proposal
WHERE prompt_proposal.clause_id = sqlc.arg('clause_id')::uuid
  AND prompt_proposal.rollback_of_version = sqlc.arg('rollback_of_version')::int;

-- name: ListCarrierBusinessColumns :many
-- Reconciliation input: every agent-tier business column in the workspace.
SELECT id AS scope_id, name AS carrier_name, instructions AS business_column, 'agent'::text AS scope
FROM agent
WHERE agent.workspace_id = sqlc.arg('workspace_id')::uuid
  AND (sqlc.narg('after_id')::uuid IS NULL OR agent.id > sqlc.narg('after_id')::uuid)
ORDER BY agent.id
LIMIT sqlc.arg('row_limit')::int;

-- name: CountWorkspaceAgents :one
SELECT COUNT(*)::bigint FROM agent
WHERE agent.workspace_id = sqlc.arg('workspace_id')::uuid;

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

-- ── Governance views (ADR 007 §9) ──────────────────────────────────────────

-- name: ListClauseOverview :many
-- One row per clause with its latest version and live binding — the
-- governance list's base set. A clause with no versions yet (impossible via
-- the enactment path, possible via manual rows) shows latest_version 0.
SELECT c.id, c.workspace_id, c.slug, c.title, c.status,
       c.created_by_type, c.created_by_id, c.created_at,
       COALESCE(cv.max_version, 0)::int AS latest_version,
       b.scope AS bound_scope, b.scope_id AS bound_scope_id
FROM clause c
LEFT JOIN (
    SELECT clause_id, MAX(version) AS max_version
    FROM clause_version GROUP BY clause_id
) cv ON cv.clause_id = c.id
LEFT JOIN clause_binding b ON b.clause_id = c.id AND b.status = 'active'
WHERE c.workspace_id = sqlc.arg('workspace_id')::uuid
ORDER BY c.slug;

-- name: CountWorkspaceInjections :one
-- Dynamic-path health over a trailing window: total injections, covered
-- runs, and degraded counts. Carrier observations also count degraded runs
-- when fallback parsing yielded no clause-level rows.
WITH degraded_tasks AS (
    SELECT task_id
    FROM clause_injection
    WHERE workspace_id = sqlc.arg('workspace_id')::uuid
      AND created_at > sqlc.arg('since')::timestamptz
      AND degraded
    UNION
    SELECT task_id
    FROM clause_injection_observation
    WHERE workspace_id = sqlc.arg('workspace_id')::uuid
      AND created_at > sqlc.arg('since')::timestamptz
      AND degraded
)
SELECT COUNT(*)::bigint AS injections,
       COUNT(DISTINCT task_id)::bigint AS runs,
       COUNT(*) FILTER (WHERE degraded)::bigint AS degraded_rows,
       (SELECT COUNT(*)::bigint FROM degraded_tasks) AS degraded_runs
FROM clause_injection
WHERE clause_injection.workspace_id = sqlc.arg('workspace_id')::uuid
  AND clause_injection.created_at > sqlc.arg('since')::timestamptz;

-- name: SumWorkspaceClauseTokens :one
-- D1 across the whole clause asset library over a window: the numerator of
-- the governance view's token share. NULL daily rows roll up as 0 here by
-- design — the per-clause reads keep the NULL distinction.
SELECT COALESCE(SUM(injected_tokens), 0)::bigint AS total_tokens
FROM clause_quality_daily
WHERE clause_quality_daily.workspace_id = sqlc.arg('workspace_id')::uuid
  AND clause_quality_daily.day >= sqlc.arg('since_day')::date;

-- name: CountBoundCarriersByScope :many
-- Migration coverage: carriers with at least one live clause binding, per
-- tier. The denominator (all carriers) is the caller's row counts.
SELECT b.scope, COUNT(DISTINCT b.scope_id)::bigint AS carriers
FROM clause_binding b
JOIN clause c ON c.id = b.clause_id
WHERE c.workspace_id = sqlc.arg('workspace_id')::uuid
  AND b.status = 'active'
GROUP BY b.scope;

-- name: ListRollbackSuggestions :many
-- The governance overview's rollback-suggestion list: every proposal the
-- statistics path ever drafted, newest first.
SELECT * FROM prompt_proposal
WHERE prompt_proposal.workspace_id = sqlc.arg('workspace_id')::uuid
  AND prompt_proposal.rollback_of_version IS NOT NULL
ORDER BY prompt_proposal.created_at DESC
LIMIT 50;

-- name: ListClauseInjectionLayerViolations :many
-- 层集合 ⊆ invariant (ADR 007 QA plan): every injection's carrier layer must
-- appear in the run's prompt_versions attribution. A violation means the
-- injection path and the attribution path disagree — per-layer rollups are
-- not trustworthy until it is explained.
SELECT ci.task_id, ci.clause_id, ci.clause_version, ci.carrier_scope,
       ci.degraded, ci.created_at
FROM clause_injection ci
JOIN agent_task_queue atq ON atq.id = ci.task_id
WHERE ci.workspace_id = sqlc.arg('workspace_id')::uuid
  AND ci.created_at > sqlc.arg('since')::timestamptz
  AND (atq.prompt_versions -> ci.carrier_scope) IS NULL
LIMIT sqlc.arg('row_limit')::int;

-- name: ListDegradedClauseCarrierScopes :many
-- The observation table is authoritative. The second arm covers legacy rows
-- only where the historical carrier ID is stable on the task. Project IDs are
-- intentionally omitted: legacy task rows do not retain which project was
-- read during claim, and current issue/session/autopilot relations may move.
SELECT DISTINCT scope, scope_id
FROM (
    SELECT cio.carrier_scope AS scope, cio.carrier_scope_id::text AS scope_id
    FROM clause_injection_observation cio
    WHERE cio.workspace_id = sqlc.arg('workspace_id')::uuid
      AND cio.created_at > sqlc.arg('since')::timestamptz
      AND cio.degraded = TRUE
    UNION ALL
    SELECT ci.carrier_scope AS scope,
           CASE ci.carrier_scope
             WHEN 'workspace' THEN a.workspace_id
             WHEN 'project' THEN NULL::uuid
             WHEN 'squad' THEN atq.squad_id
             WHEN 'agent' THEN atq.agent_id
           END::text AS scope_id
    FROM clause_injection ci
    JOIN agent_task_queue atq ON atq.id = ci.task_id
    JOIN agent a ON a.id = atq.agent_id
    WHERE ci.workspace_id = sqlc.arg('workspace_id')::uuid
      AND ci.created_at > sqlc.arg('since')::timestamptz
      AND ci.degraded = TRUE
      AND NOT EXISTS (
          SELECT 1 FROM clause_injection_observation cio
          WHERE cio.task_id = ci.task_id
            AND cio.carrier_scope = ci.carrier_scope
            AND cio.degraded
      )
) degraded_carriers
WHERE scope_id IS NOT NULL;

-- name: SumClauseInjectionStatsByClause :many
-- Per-clause window stats for the governance list, one pass instead of one
-- query per clause.
SELECT clause_id,
       COUNT(*)::bigint AS injections,
       COUNT(DISTINCT task_id)::bigint AS runs
FROM clause_injection
WHERE clause_injection.workspace_id = sqlc.arg('workspace_id')::uuid
  AND clause_injection.created_at > sqlc.arg('since')::timestamptz
GROUP BY clause_id;

-- name: SumClauseTokensByClause :many
-- Per-clause D1 window sums for the governance list. An absent row means
-- "no rollup yet", not zero — the caller renders it as pending.
SELECT clause_id, COALESCE(SUM(injected_tokens), 0)::bigint AS total_tokens
FROM clause_quality_daily
WHERE clause_quality_daily.workspace_id = sqlc.arg('workspace_id')::uuid
  AND clause_quality_daily.day >= sqlc.arg('since_day')::date
GROUP BY clause_id;
