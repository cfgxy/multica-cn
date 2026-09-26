-- Prompt quiz bank and measurements (RUYI-185, self-evolution phase 3).
--
-- Nothing here divides, and nothing here decides. The baseline comparison is a
-- two-sample test over the raw per-measurement values; SQL hands those values
-- up unfolded and pkg/promptquiz does the statistics, so the decision rule is
-- unit-testable against a fixed sample without a database.

-- name: CreatePromptQuizItem :one
INSERT INTO prompt_quiz_item (
    workspace_id, slug, title, body, runtime_profile, created_by_user_id
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('slug'), sqlc.arg('title'), sqlc.arg('body'),
    sqlc.arg('runtime_profile'), sqlc.narg('created_by_user_id')
)
RETURNING *;

-- name: UpdatePromptQuizItem :one
-- revision advances only when the body actually changed: re-titling an item
-- must not orphan the measurements already taken against its wording, and
-- bumping on every save would do exactly that.
UPDATE prompt_quiz_item
SET title = sqlc.arg('title'),
    body = sqlc.arg('body'),
    runtime_profile = sqlc.arg('runtime_profile'),
    active = sqlc.arg('active'),
    revision = CASE WHEN body = sqlc.arg('body') THEN revision ELSE revision + 1 END,
    updated_at = now()
WHERE id = sqlc.arg('id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: DeletePromptQuizItem :execrows
DELETE FROM prompt_quiz_item
WHERE id = sqlc.arg('id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: GetPromptQuizItem :one
SELECT *
FROM prompt_quiz_item
WHERE id = sqlc.arg('id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: ListPromptQuizItems :many
SELECT *
FROM prompt_quiz_item
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND (NOT sqlc.arg('active_only')::boolean OR active)
ORDER BY created_at DESC;

-- name: ListActivePromptQuizItemsForProfile :many
-- What one sweep asks. Ordered by id so two sweeps over an unchanged bank
-- enumerate in the same order, which is what makes the job's enqueue step
-- replayable.
SELECT *
FROM prompt_quiz_item
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND active
  AND runtime_profile = sqlc.arg('runtime_profile')::text
ORDER BY id;

-- name: UpsertPromptQuizResult :one
-- Collection is keyed on task_id, so a sweep that is re-entered after a crash
-- rewrites the measurement it already wrote instead of doubling the sample —
-- the property the distribution baseline depends on, since N is counted from
-- these rows.
INSERT INTO prompt_quiz_result (
    workspace_id, scope, scope_id, version,
    item_id, item_revision, item_body_sha256,
    batch_id, task_id, outcome, run_tokens, duration_ms, measured_at
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('scope'), sqlc.arg('scope_id'), sqlc.arg('version'),
    sqlc.arg('item_id'), sqlc.arg('item_revision'), sqlc.arg('item_body_sha256'),
    sqlc.arg('batch_id'), sqlc.arg('task_id'), sqlc.arg('outcome'),
    sqlc.narg('run_tokens'), sqlc.narg('duration_ms'), now()
)
ON CONFLICT (task_id) DO UPDATE SET
    outcome = EXCLUDED.outcome,
    run_tokens = EXCLUDED.run_tokens,
    duration_ms = EXCLUDED.duration_ms,
    measured_at = now()
RETURNING *;

-- name: ListPromptQuizSamples :many
-- The raw sample group for one (scope, version): one row per measurement, not
-- an aggregate. This is the query prompt_quality_daily cannot serve — it stores
-- the median of a day and has already discarded the per-repeat values the
-- two-sample test needs.
--
-- errored measurements are returned with their outcome intact rather than
-- filtered in SQL: whether a runaway belongs in the sample is a decision the
-- Go layer makes and documents, and hiding it here would make that decision
-- invisible.
SELECT
    r.item_id,
    r.item_revision,
    r.item_body_sha256,
    r.outcome,
    r.run_tokens,
    r.duration_ms,
    r.measured_at
FROM prompt_quiz_result r
WHERE r.scope = sqlc.arg('scope')::text
  AND r.scope_id = sqlc.arg('scope_id')::uuid
  AND r.version = sqlc.arg('version')::int
ORDER BY r.measured_at DESC
LIMIT sqlc.arg('row_limit')::int;

-- name: CountPromptQuizMeasurementsForVersion :one
-- How far this version is from the required repeat count N.
--
-- Two numbers, because a measurement exists in two states and the sweep must
-- subtract both. graded counts stored results, excluding 'errored': a run that
-- never answered measured nothing about the prompt and must not let the sweep
-- stop early. in_flight counts the runs this or an earlier tick already
-- ordered and that have not finished yet — without it, consecutive ticks over
-- the same version re-order the full remainder every hour until the first run
-- lands, and the version ends up with several times N measurements. An
-- in-flight run is counted by agent rather than by version because the version
-- it measures is decided when the daemon claims it, not when it is ordered; the
-- scope guard is what makes that identification valid, so a non-agent scope
-- reads zero rather than another scope's runs.
SELECT
    (
        SELECT count(*)::int
        FROM prompt_quiz_result r
        WHERE r.scope = sqlc.arg('scope')::text
          AND r.scope_id = sqlc.arg('scope_id')::uuid
          AND r.version = sqlc.arg('version')::int
          AND r.outcome <> 'errored'
    ) AS graded,
    (
        SELECT count(*)::int
        FROM agent_task_queue atq
        WHERE sqlc.arg('scope')::text = 'agent'
          AND atq.agent_id = sqlc.arg('scope_id')::uuid
          AND atq.originator_source = 'quiz'
          AND atq.issue_id IS NULL
          AND atq.status IN ('queued', 'dispatched', 'running')
    ) AS in_flight;

-- name: ListPromptQuizBatchOutcomes :many
SELECT outcome, count(*)::int AS total
FROM prompt_quiz_result
WHERE batch_id = sqlc.arg('batch_id')::uuid
GROUP BY outcome;

-- name: CreatePromptQuizTask :one
-- A quiz run, enqueued on the existing no-issue path. issue_id is NULL by
-- construction, which is what keeps it out of every issue-dimension query
-- (those all JOIN issue), and originator_source='quiz' is what keeps it out of
-- production run statistics.
--
-- Deliberately NOT going through lock_task_owner_rows like CreateQuickCreateTask
-- does: that lock serialises a human's concurrent creates against the same
-- owner rows, and a sweep is already serialised by the scheduler advisory lock.
INSERT INTO agent_task_queue (
    agent_id,
    runtime_id,
    issue_id,
    status,
    priority,
    context,
    originator_source,
    trigger_evidence_kind,
    trigger_evidence_ref_id
) VALUES (
    sqlc.arg('agent_id'), sqlc.arg('runtime_id'), NULL, 'queued', sqlc.arg('priority'),
    sqlc.arg('context'), 'quiz', 'quiz_item', sqlc.arg('item_id')
)
RETURNING *;

-- name: ListFinishedPromptQuizTasks :many
-- Quiz runs that reached a terminal state and have no measurement row yet.
-- Driven off originator_source so the collector can never pick up a production
-- run, even one that also has no issue.
SELECT
    atq.id AS task_id,
    atq.agent_id,
    atq.status,
    atq.trigger_evidence_ref_id AS item_id,
    atq.context,
    atq.prompt_versions,
    atq.started_at,
    atq.completed_at,
    a.workspace_id,
    -- Measured-vs-zero as a pair, the same way ListPromptQualityRunsForDay
    -- reports it: a terminal run whose usage rows never arrived measured no
    -- cost, and collapsing that into 0 would let a reporting outage read as a
    -- cost improvement in the baseline comparison.
    (tu.total_tokens IS NOT NULL)::boolean AS usage_measured,
    COALESCE(tu.total_tokens, 0)::bigint AS run_tokens
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN LATERAL (
    SELECT SUM(u.input_tokens + u.output_tokens)::bigint AS total_tokens
    FROM task_usage u
    WHERE u.task_id = atq.id
) tu ON TRUE
WHERE atq.originator_source = 'quiz'
  AND atq.issue_id IS NULL
  AND atq.status IN ('completed', 'failed', 'cancelled')
  AND atq.completed_at IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM prompt_quiz_result r WHERE r.task_id = atq.id
  )
ORDER BY atq.completed_at
LIMIT sqlc.arg('row_limit')::int;

-- name: CountActivePromptQuizTasks :one
-- Back-pressure: a sweep does not enqueue while its predecessor's runs are
-- still in flight, otherwise a slow deployment accumulates quiz runs faster
-- than it drains them.
SELECT count(*)::int AS active
FROM agent_task_queue
WHERE originator_source = 'quiz'
  AND status IN ('queued', 'dispatched', 'running');

-- name: GetPromptQuizSweepState :one
SELECT last_run_started_at, last_run_finished_at, last_enqueued, last_collected, last_error
FROM prompt_quiz_sweep_state
WHERE id = 1;

-- name: RecordPromptQuizSweep :exec
UPDATE prompt_quiz_sweep_state
SET last_run_started_at = sqlc.arg('started_at')::timestamptz,
    last_run_finished_at = now(),
    last_enqueued = sqlc.arg('enqueued')::int,
    last_collected = sqlc.arg('collected')::int,
    last_error = sqlc.narg('last_error')
WHERE id = 1;

-- name: ListPromptQuizScopesForSweep :many
-- Which (agent scope, version) pairs the sweep measures: the current prompt
-- version of every agent that has a quiz-eligible runtime.
--
-- Driven from prompt_version rather than from agent so a version with no runs
-- yet is still measured — that is the whole point of a quiz, which is what
-- separates it from the phase-2 rollup's run-driven enumeration.
SELECT DISTINCT ON (pv.scope_id)
    pv.scope_id AS agent_id,
    pv.version,
    a.workspace_id,
    a.runtime_id
FROM prompt_version pv
JOIN agent a ON a.id = pv.scope_id
WHERE pv.scope = 'agent'
  AND a.archived_at IS NULL
  AND a.runtime_id IS NOT NULL
ORDER BY pv.scope_id, pv.version DESC
LIMIT sqlc.arg('row_limit')::int;
