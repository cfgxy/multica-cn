-- Prompt quality rollup take-off queries (RUYI-184, dimensions D1/D2/D4/D5/D6/D7).
--
-- THE SHAPE OF EVERY QUERY HERE:
--
-- A run is attributed to one (scope, scope_id, version, day) bucket per prompt
-- tier it was claimed with. agent_task_queue.prompt_versions (migration 917)
-- holds {tier: version} with a key present only for tiers actually injected,
-- so expanding it with jsonb_each_text is what fans one run out into its
-- buckets — and a tier the run never saw simply has no key, contributing to no
-- bucket at all rather than to "version 0".
--
-- scope_id is resolved per tier: workspace from the agent (agent_task_queue
-- has no workspace_id column, which is why every query here joins agent),
-- agent from the task, squad from the task, project from the issue. A run
-- whose issue has no project cannot be attributed to a project bucket, so its
-- scope_id comes out NULL and it is filtered out.
--
-- WHY COUNTS AND NOT RATES:
--
-- Nothing here divides. The job persists numerators and denominators and lets
-- the read path decide what "no data" looks like; a rate computed in SQL would
-- have to pick between 0 and NULL for an empty denominator, and both are
-- wrong answers to a question the dashboard has to be able to leave blank.

-- name: ListPromptQualityDirtyBuckets :many
-- Buckets touched by a run that finished after the watermark. The job
-- recomputes each of these from scratch — not incrementally — so re-scanning a
-- bucket converges instead of double-counting, which is what lets this rollup
-- run off a plain watermark with no dirty queue (see migration 925).
--
-- @limit bounds one tick's work: a backfill over a long-idle deployment walks
-- the history in bounded steps rather than holding one enormous transaction.
WITH run_scope AS (
    SELECT
        a.workspace_id,
        kv.key::text AS scope,
        CASE kv.key
            WHEN 'workspace' THEN a.workspace_id
            WHEN 'agent' THEN atq.agent_id
            WHEN 'squad' THEN atq.squad_id
            WHEN 'project' THEN i.project_id
        END::uuid AS scope_id,
        kv.value::int AS version,
        (atq.completed_at AT TIME ZONE 'UTC')::date AS day,
        atq.completed_at
    FROM agent_task_queue atq
    JOIN agent a ON a.id = atq.agent_id
    LEFT JOIN issue i ON i.id = atq.issue_id
    CROSS JOIN LATERAL jsonb_each_text(atq.prompt_versions) AS kv(key, value)
    WHERE atq.status IN ('completed', 'failed', 'cancelled')
      AND atq.completed_at IS NOT NULL
      AND atq.completed_at > sqlc.arg('watermark')::timestamptz
      AND kv.key IN ('workspace', 'project', 'squad', 'agent')
)
SELECT
    workspace_id,
    scope,
    scope_id,
    version,
    day,
    MAX(completed_at)::timestamptz AS max_completed_at
FROM run_scope
WHERE scope_id IS NOT NULL
  AND version > 0
GROUP BY workspace_id, scope, scope_id, version, day
ORDER BY MAX(completed_at)
LIMIT sqlc.arg('row_limit')::int;

-- name: ListPromptQualityBucketRuns :many
-- Every run in one bucket, with the per-run facts D1/D5/D6 need and the issue
-- id D7 needs. One row per run; task_usage is pre-aggregated because a run has
-- one usage row per (provider, model) and the D1 statistic is the run's total.
--
-- input_tokens is the BILLING counter: it accumulates every request the run
-- made, so a long run that resent its history counts that history repeatedly.
-- That is the right denominator for "what did this prompt cost us", which is
-- the D1 question — context_tokens (migration 908) answers a different one.
--
-- usage_measured distinguishes a run with no usage row at all (the daemon
-- never reported) from one that genuinely burned nothing; the rollup keeps
-- only measured runs in the median.
SELECT
    atq.id AS task_id,
    atq.issue_id,
    atq.status,
    COALESCE(NULLIF(atq.failure_reason, ''), '')::text AS failure_reason,
    atq.attempt,
    (tu.total_tokens IS NOT NULL)::boolean AS usage_measured,
    COALESCE(tu.total_tokens, 0)::bigint AS run_tokens
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
CROSS JOIN LATERAL jsonb_each_text(atq.prompt_versions) AS kv(key, value)
LEFT JOIN LATERAL (
    SELECT SUM(u.input_tokens + u.output_tokens)::bigint AS total_tokens
    FROM task_usage u
    WHERE u.task_id = atq.id
) tu ON TRUE
WHERE atq.status IN ('completed', 'failed', 'cancelled')
  AND atq.completed_at IS NOT NULL
  AND (atq.completed_at AT TIME ZONE 'UTC')::date = sqlc.arg('day')::date
  AND kv.key = sqlc.arg('scope')::text
  AND kv.value::int = sqlc.arg('version')::int
  AND CASE sqlc.arg('scope')::text
        WHEN 'workspace' THEN a.workspace_id
        WHEN 'agent' THEN atq.agent_id
        WHEN 'squad' THEN atq.squad_id
        WHEN 'project' THEN i.project_id
      END = sqlc.arg('scope_id')::uuid;

-- name: CountPromptQualityToolResults :one
-- D4 numerator/denominator for one bucket's runs.
--
-- The FILTER on is_error IS NOT NULL is the whole point: a tool result whose
-- runtime never reported the flag — every result written before migration 924,
-- and every backend that does not surface one — is in NEITHER count. measured
-- therefore reads as "how much of this bucket D4 can actually speak about",
-- and measured = 0 is the "no data" state the dashboard renders blank instead
-- of as 0% (ADR-002 T5). This history is not backfillable; the flag was never
-- persisted.
SELECT
    COUNT(*) FILTER (WHERE m.is_error IS NOT NULL)::bigint AS measured,
    COUNT(*) FILTER (WHERE m.is_error)::bigint AS errored
FROM task_message m
WHERE m.task_id = ANY (sqlc.arg('task_ids')::uuid[])
  AND m.type = 'tool_result';

-- name: ListPromptQualityDisciplineMessages :many
-- D2 raw material: the shell invocations of one bucket's runs.
--
-- Only tool_use rows are read, and only the ones the discipline rule table can
-- decide on. The command text leaves the database to be matched in Go and is
-- never written back — what lands in prompt_quality_daily is a rule id, a
-- weight and a seq (see pkg/promptdiscipline).
SELECT
    m.task_id,
    m.seq,
    m.type,
    COALESCE(m.tool, '') AS tool,
    m.input
FROM task_message m
WHERE m.task_id = ANY (sqlc.arg('task_ids')::uuid[])
  AND m.type = 'tool_use'
ORDER BY m.task_id, m.seq;

-- name: ListPromptQualityIssueReviewOutcomes :many
-- D7 first-pass rate, issue-level coarse measure (ADR-002 §2.7, explicitly
-- low confidence).
--
-- activity_log records status transitions as action='status_changed' with
-- details {"from": ..., "to": ...} (see cmd/server/activity_listeners.go), so
-- "reached review" is any transition whose `to` is in_review, and "was sent
-- back" is any transition from in_review to in_progress. An issue that never
-- reached review is absent from this result entirely rather than counted as a
-- failure — it has not been judged yet, and counting it either way would be
-- inventing a verdict.
SELECT
    al.issue_id,
    COUNT(*) FILTER (WHERE al.details->>'to' = 'in_review')::int AS entered_review,
    COUNT(*) FILTER (
        WHERE al.details->>'from' = 'in_review'
          AND al.details->>'to' = 'in_progress'
    )::int AS sent_back
FROM activity_log al
WHERE al.issue_id = ANY (sqlc.arg('issue_ids')::uuid[])
  AND al.action = 'status_changed'
GROUP BY al.issue_id;

-- name: GetPromptVersionContent :one
-- D1 static half: the content this version injects. The rollup measures its
-- size and stores only the number — the body never reaches prompt_quality_daily.
SELECT content
FROM prompt_version
WHERE scope = sqlc.arg('scope')::text
  AND scope_id = sqlc.arg('scope_id')::uuid
  AND version = sqlc.arg('version')::int;

-- name: UpsertPromptQualityDaily :exec
-- Writes one recomputed bucket. ON CONFLICT overwrites every measure rather
-- than adding to it, which is what makes a re-scanned bucket converge — the
-- property migration 925 relies on to justify having no dirty queue.
INSERT INTO prompt_quality_daily (
    workspace_id, scope, scope_id, version, day,
    finished_runs,
    injected_tokens, run_tokens_median,
    discipline_score_median, discipline_covered_runs, discipline_deductions,
    tool_results_measured, tool_results_error,
    attempt_total, retried_runs,
    attributable_failed_runs, excluded_failed_runs, failure_reason_counts,
    first_pass_issues, reviewed_issues,
    updated_at
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('scope'), sqlc.arg('scope_id'), sqlc.arg('version'), sqlc.arg('day'),
    sqlc.arg('finished_runs'),
    sqlc.narg('injected_tokens'), sqlc.narg('run_tokens_median'),
    sqlc.narg('discipline_score_median'), sqlc.arg('discipline_covered_runs'), sqlc.arg('discipline_deductions'),
    sqlc.arg('tool_results_measured'), sqlc.arg('tool_results_error'),
    sqlc.arg('attempt_total'), sqlc.arg('retried_runs'),
    sqlc.arg('attributable_failed_runs'), sqlc.arg('excluded_failed_runs'), sqlc.arg('failure_reason_counts'),
    sqlc.arg('first_pass_issues'), sqlc.arg('reviewed_issues'),
    now()
)
ON CONFLICT (scope, scope_id, version, day) DO UPDATE SET
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

-- name: GetPromptQualityWatermark :one
SELECT watermark_at, last_run_started_at, last_run_finished_at, last_run_rows, last_error
FROM prompt_quality_rollup_state
WHERE id = 1;

-- name: AdvancePromptQualityWatermark :exec
-- Moves the watermark to the newest completed_at the tick actually consumed.
-- GREATEST guards against a late-arriving row dragging it backwards, which
-- would make the next tick re-scan history it already converged.
UPDATE prompt_quality_rollup_state
SET watermark_at = GREATEST(watermark_at, sqlc.arg('watermark')::timestamptz),
    last_run_finished_at = now(),
    last_run_rows = sqlc.arg('rows_affected')::bigint,
    last_error = NULL
WHERE id = 1;

-- name: ListPromptQualityDaily :many
-- Read path: one scope's rollup over a day window, newest first. A single
-- index scan of idx_prompt_quality_daily_scope_day (migration 926) — the
-- reason the rollup exists at all.
SELECT *
FROM prompt_quality_daily
WHERE scope = sqlc.arg('scope')::text
  AND scope_id = sqlc.arg('scope_id')::uuid
  AND day >= sqlc.arg('since')::date
ORDER BY day DESC, version DESC;

-- name: ListPromptPerplexityScores :many
-- D3 read path: every runtime profile's score for one scope, newest first.
-- Returned as separate rows on purpose — the profiles are scored against
-- different assembled prompts and the dashboard shows them as separate
-- sub-tabs; nothing downstream may average them (Owner Q17).
SELECT *
FROM prompt_perplexity_score
WHERE scope = sqlc.arg('scope')::text
  AND scope_id = sqlc.arg('scope_id')::uuid
ORDER BY version DESC, runtime_profile;

-- name: UpsertPromptPerplexityScore :one
INSERT INTO prompt_perplexity_score (
    workspace_id, scope, scope_id, version, runtime_profile,
    band, percent_low, percent_high, evidence, model, scored_at
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('scope'), sqlc.arg('scope_id'), sqlc.arg('version'), sqlc.arg('runtime_profile'),
    sqlc.arg('band'), sqlc.arg('percent_low'), sqlc.arg('percent_high'), sqlc.arg('evidence'), sqlc.arg('model'), now()
)
ON CONFLICT (scope, scope_id, version, runtime_profile) DO UPDATE SET
    band = EXCLUDED.band,
    percent_low = EXCLUDED.percent_low,
    percent_high = EXCLUDED.percent_high,
    evidence = EXCLUDED.evidence,
    model = EXCLUDED.model,
    scored_at = now()
RETURNING *;

-- name: GetPromptPerplexityTierScopes :one
-- D3 assembly: the other tiers an agent's run reads alongside its own
-- instructions. agent_task_queue is not consulted here — D3 scores a VERSION,
-- not a run, so the tiers come from the agent's current placement.
--
-- squad_id is the agent's squad when it has exactly one; an agent in several
-- squads yields NULL rather than an arbitrary pick, and the leader_task
-- profile then scores without a squad tier instead of with the wrong one.
SELECT
    a.workspace_id,
    (
        SELECT (array_agg(sm.squad_id))[1]
        FROM squad_member sm
        JOIN squad s ON s.id = sm.squad_id AND s.archived_at IS NULL
        WHERE sm.member_type = 'agent' AND sm.member_id = a.id
        HAVING count(*) = 1
    )::uuid AS squad_id
FROM agent a
WHERE a.id = sqlc.arg('agent_id')::uuid;

-- name: ListPromptPerplexityUnscoredVersions :many
-- D3 orchestration: agent prompt versions that carry finished runs but have no
-- score for at least one runtime profile.
--
-- Driven from prompt_quality_daily rather than from prompt_version so a
-- version nobody ever ran is not scored: a score costs a model call, and the
-- dashboard only ever shows D3 next to versions that have runs beside it.
--
-- The NOT EXISTS counts profiles rather than rows because a version scored for
-- `member` but refused for `leader_task` is still incomplete, and rescoring is
-- an upsert.
SELECT
    d.workspace_id,
    d.scope_id AS agent_id,
    d.version,
    max(d.day)::date AS last_day
FROM prompt_quality_daily d
WHERE d.scope = 'agent'
  AND d.finished_runs > 0
  AND (
    SELECT count(*)
    FROM prompt_perplexity_score s
    WHERE s.scope = 'agent'
      AND s.scope_id = d.scope_id
      AND s.version = d.version
  ) < sqlc.arg('profile_count')::int
GROUP BY d.workspace_id, d.scope_id, d.version
ORDER BY last_day DESC, d.version DESC
LIMIT sqlc.arg('row_limit')::int;
