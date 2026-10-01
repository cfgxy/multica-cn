-- Prompt quiz bank and measurements (RUYI-185, self-evolution phase 3).
--
-- Nothing here divides, and nothing here decides. The baseline comparison is a
-- two-sample test over the raw per-measurement values; SQL hands those values
-- up unfolded and pkg/promptquiz does the statistics, so the decision rule is
-- unit-testable against a fixed sample without a database.

-- name: CreatePromptQuizItem :one
INSERT INTO prompt_quiz_item (
    workspace_id, slug, title, body, rubric, rubric_checks, tags, difficulty, runtime_profile, created_by_user_id
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('slug'), sqlc.arg('title'), sqlc.arg('body'),
    sqlc.arg('rubric'), sqlc.arg('rubric_checks'), sqlc.arg('tags'), sqlc.arg('difficulty'),
    sqlc.arg('runtime_profile'), sqlc.narg('created_by_user_id')
)
RETURNING *;

-- name: UpsertPromptQuizBankItem :one
-- Bank import (RUYI-286): upsert on the item's (workspace_id, slug) identity so
-- re-importing the benchmark catalog updates the shipped wording in place
-- instead of duplicating it. Revision semantics mirror UpdatePromptQuizItem: the
-- body is the only field whose change orphans old measurements, so only a body
-- change advances it — a rubric/checks/tags/difficulty edit regrades future
-- runs and re-renders the bank without rewriting history. created_by_user_id
-- keeps the original author on conflict: an import refreshes content, it does
-- not re-attribute it.
INSERT INTO prompt_quiz_item (
    workspace_id, slug, title, body, rubric, rubric_checks, tags, difficulty, runtime_profile, created_by_user_id
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('slug'), sqlc.arg('title'), sqlc.arg('body'),
    sqlc.arg('rubric'), sqlc.arg('rubric_checks'), sqlc.arg('tags'), sqlc.arg('difficulty'),
    sqlc.arg('runtime_profile'), sqlc.narg('created_by_user_id')
)
ON CONFLICT (workspace_id, slug) DO UPDATE SET
    title = EXCLUDED.title,
    body = EXCLUDED.body,
    rubric = EXCLUDED.rubric,
    rubric_checks = EXCLUDED.rubric_checks,
    tags = EXCLUDED.tags,
    difficulty = EXCLUDED.difficulty,
    revision = CASE WHEN prompt_quiz_item.body = EXCLUDED.body
                    THEN prompt_quiz_item.revision
                    ELSE prompt_quiz_item.revision + 1 END,
    updated_at = now()
RETURNING *;

-- name: UpdatePromptQuizItem :one
-- revision advances only when the BODY actually changed: re-titling an item, or
-- rewriting its rubric, must not orphan the measurements already taken against
-- its wording, and bumping on every save would do exactly that. The rubric is
-- deliberately not part of the test: it never reached the run being measured, so
-- editing it changes nothing about what was measured. rubric_checks follows the
-- rubric for the same reason — it grades answers, it is not part of what was
-- measured (migration 950).
UPDATE prompt_quiz_item
SET title = sqlc.arg('title'),
    body = sqlc.arg('body'),
    rubric = sqlc.arg('rubric'),
    rubric_checks = sqlc.arg('rubric_checks'),
    tags = sqlc.arg('tags'),
    difficulty = sqlc.arg('difficulty'),
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

-- name: GetPromptQuizItemWorkspace :one
-- Workspace resolution for quiz runs (RUYI-286 rework). ResolveTaskWorkspaceID
-- knows only the task, so this is the one item read that cannot be
-- workspace-scoped: it looks the workspace UP from the item id the task's
-- context carries, instead of filtering by one. Selects the id alone — the
-- private halves must not travel on an access-control path.
SELECT workspace_id
FROM prompt_quiz_item
WHERE id = sqlc.arg('id')::uuid;

-- name: ListPromptQuizItems :many
-- The member-visible bank. Columns are named rather than selected with * so that
-- rubric — the private half (migration 935) — cannot reach this response by
-- being added to the table: the Go type this generates simply has no field for
-- it. The same holds for rubric_checks (migration 950), the structured half of
-- the answer key: tags and difficulty are member-visible presentation metadata
-- and DO travel; the two answer-key halves never do. Reading a private half
-- goes through GetPromptQuizItem, which the router puts behind the owner role.
SELECT
    id,
    workspace_id,
    slug,
    title,
    body,
    revision,
    tags,
    difficulty,
    runtime_profile,
    active,
    created_by_user_id,
    created_at,
    updated_at
FROM prompt_quiz_item
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND (NOT sqlc.arg('active_only')::boolean OR active)
ORDER BY created_at DESC;

-- name: ListActivePromptQuizItemsForProfile :many
-- What one sweep asks. Ordered by id so two sweeps over an unchanged bank
-- enumerate in the same order, which is what makes the job's enqueue step
-- replayable.
--
-- rubric is omitted for the same reason as above, and here it is the load-bearing
-- half of the A2 guarantee: this is the only query the enqueue path reads items
-- through, so the task payload it builds CANNOT carry the private half — there is
-- no field on the row to read it from.
SELECT
    id,
    workspace_id,
    slug,
    title,
    body,
    revision,
    runtime_profile,
    active,
    created_at,
    updated_at
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
--
-- score / score_detail / graded_at (migration 950) ride the same upsert: the
-- grade is computed by the collector just before this call, so a re-collected
-- task re-grades instead of keeping a stale verdict. A NULL score is a stored
-- state, not an omission: an item without checks, an errored run, and a run
-- that left no answer text are all "measured but not graded".
INSERT INTO prompt_quiz_result (
    workspace_id, scope, scope_id, version,
    item_id, item_revision, item_body_sha256,
    runtime_id, run_model,
    batch_id, task_id, outcome, run_tokens, duration_ms, measured_at,
    score, score_detail, graded_at
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('scope'), sqlc.arg('scope_id'), sqlc.arg('version'),
    sqlc.arg('item_id'), sqlc.arg('item_revision'), sqlc.arg('item_body_sha256'),
    sqlc.arg('runtime_id'), sqlc.narg('run_model'),
    sqlc.arg('batch_id'), sqlc.arg('task_id'), sqlc.arg('outcome'),
    sqlc.narg('run_tokens'), sqlc.narg('duration_ms'), now(),
    sqlc.narg('score'), sqlc.narg('score_detail'), sqlc.narg('graded_at')
)
ON CONFLICT (task_id) DO UPDATE SET
    outcome = EXCLUDED.outcome,
    run_tokens = EXCLUDED.run_tokens,
    run_model = EXCLUDED.run_model,
    duration_ms = EXCLUDED.duration_ms,
    score = EXCLUDED.score,
    score_detail = EXCLUDED.score_detail,
    graded_at = EXCLUDED.graded_at,
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
--
-- The cohort key travels with every row — which question in which wording
-- (item_id / item_revision / item_body_sha256) and which runtime executed it
-- (runtime_id / run_model). Grouping happens in promptquiz.Cohort rather than
-- here: the rule is that the CURRENT version's newest reading defines the
-- measuring stick and the baseline version is re-read through the same one, which
-- is a decision about two result sets and cannot be expressed inside either
-- query. Newest first is load-bearing for that — CohortOf reads the order.
SELECT
    r.item_id,
    r.item_revision,
    r.item_body_sha256,
    r.runtime_id,
    r.run_model,
    r.outcome,
    r.score,
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
--
-- graded counts THE COHORT, not the table. It has to answer the same question
-- the reader answers — how many comparable readings does this version have — or
-- the sweep stops topping up a sample the baseline endpoint will then refuse as
-- too small: 30 readings spread over three bank edits and two models are not 30
-- readings. The three CTEs below are promptquiz.CohortOf and Cohort.Select
-- expressed in SQL, in the same order: pick the stick from the newest reading,
-- keep the readings taken with it, then keep each question's newest wording
-- within those. IS NOT DISTINCT FROM, not =, because an unattributed row's NULL
-- is a cohort key of its own and must match only itself.
WITH stick AS (
    SELECT r.runtime_id, r.run_model
    FROM prompt_quiz_result r
    WHERE r.scope = sqlc.arg('scope')::text
      AND r.scope_id = sqlc.arg('scope_id')::uuid
      AND r.version = sqlc.arg('version')::int
      AND r.outcome <> 'errored'
      AND r.run_tokens IS NOT NULL
    ORDER BY r.measured_at DESC
    LIMIT 1
),
on_stick AS (
    SELECT r.item_id, r.item_revision, r.item_body_sha256, r.measured_at
    FROM prompt_quiz_result r, stick s
    WHERE r.scope = sqlc.arg('scope')::text
      AND r.scope_id = sqlc.arg('scope_id')::uuid
      AND r.version = sqlc.arg('version')::int
      AND r.outcome <> 'errored'
      AND r.run_tokens IS NOT NULL
      AND r.runtime_id IS NOT DISTINCT FROM s.runtime_id
      AND r.run_model IS NOT DISTINCT FROM s.run_model
),
in_cohort AS (
    SELECT
        item_revision,
        item_body_sha256,
        first_value(item_revision) OVER (PARTITION BY item_id ORDER BY measured_at DESC) AS cohort_revision,
        first_value(item_body_sha256) OVER (PARTITION BY item_id ORDER BY measured_at DESC) AS cohort_sha
    FROM on_stick
)
SELECT
    (
        SELECT count(*)::int
        FROM in_cohort
        WHERE item_revision = cohort_revision
          AND item_body_sha256 = cohort_sha
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

-- name: ListPromptQuizItemDiscrimination :many
-- Per-question spread, for the "no discrimination" mark shown where the bank is
-- maintained (A4).
--
-- Only the question's CURRENT wording counts: readings taken before an edit
-- describe a question that no longer exists, and pooling them would report
-- spread the current wording never produced. Runtime is deliberately NOT part of
-- the grouping here, unlike in the baseline read — mixing runtimes can only
-- widen a question's spread, so the error it can cause is failing to mark a flat
-- question, never marking a working one. The mark invites an author to delete a
-- question, so that is the direction the bias has to point.
--
-- attempts counts every reading of the current wording and graded counts the ones
-- that produced a value; a question that errors on every run therefore shows
-- attempts without graded, which is the floor case the Go judgement reports as
-- "no signal". percentile_cont is the same inclusive linear interpolation
-- promptquiz.quantile implements, so the mark and the baseline summary describe
-- the same statistic.
WITH current_wording AS (
    SELECT DISTINCT ON (r.item_id)
        r.item_id, r.item_revision, r.item_body_sha256
    FROM prompt_quiz_result r
    WHERE r.workspace_id = sqlc.arg('workspace_id')::uuid
    ORDER BY r.item_id, r.measured_at DESC
)
SELECT
    w.item_id,
    count(*)::int AS attempts,
    count(*) FILTER (
        WHERE r.outcome <> 'errored' AND r.run_tokens IS NOT NULL
    )::int AS graded,
    COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY r.run_tokens) FILTER (
        WHERE r.outcome <> 'errored'
    ), 0)::float8 AS median,
    COALESCE(
        percentile_cont(0.75) WITHIN GROUP (ORDER BY r.run_tokens) FILTER (
            WHERE r.outcome <> 'errored'
        ) - percentile_cont(0.25) WITHIN GROUP (ORDER BY r.run_tokens) FILTER (
            WHERE r.outcome <> 'errored'
        ), 0)::float8 AS iqr
FROM current_wording w
JOIN prompt_quiz_result r
  ON r.item_id = w.item_id
 AND r.item_revision = w.item_revision
 AND r.item_body_sha256 = w.item_body_sha256
WHERE r.workspace_id = sqlc.arg('workspace_id')::uuid
GROUP BY w.item_id;

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
-- Fenced against workspace teardown: lock_task_owner_rows (migration 284) locks
-- the owners' workspace rows in this statement's own transaction and returns
-- false once they are gone, so a run enqueued while its agent's workspace is
-- being deleted writes no row instead of being stranded. The scheduler advisory
-- lock serialises sweep ticks against each other; it says nothing about a
-- teardown or a legacy runtime merge running concurrently, which is the race
-- this fence closes. Returning no row is the expected outcome then, and the
-- sweep stops ordering runs for that scope.
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
)
SELECT
    sqlc.arg('agent_id'), sqlc.arg('runtime_id'), NULL::uuid, 'queued', sqlc.arg('priority'),
    sqlc.arg('context'), 'quiz', 'quiz_item', sqlc.arg('item_id')
WHERE lock_task_owner_rows(sqlc.arg('agent_id'), NULL::uuid, sqlc.arg('runtime_id'))
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
    -- The runtime this measurement was taken on (A1). Read from the QUEUE row,
    -- not from agent: agent.runtime_id is the current binding and would
    -- re-attribute every past measurement the moment an agent is re-pointed.
    -- Migration 251 made this column nullable on terminal rows, so it can be
    -- absent; the collector skips such a run rather than storing a reading whose
    -- instrument is unknown.
    atq.runtime_id,
    -- The model the daemon actually billed the run under, comma-joined and
    -- deduplicated when a run spanned more than one. Taken from task_usage
    -- rather than from agent.model for the same reason as runtime_id, and
    -- because agent.model is what is configured while this is what ran.
    tu.models AS run_model,
    -- Measured-vs-zero as a pair, the same way ListPromptQualityRunsForDay
    -- reports it: a terminal run whose usage rows never arrived measured no
    -- cost, and collapsing that into 0 would let a reporting outage read as a
    -- cost improvement in the baseline comparison.
    (tu.total_tokens IS NOT NULL)::boolean AS usage_measured,
    COALESCE(tu.total_tokens, 0)::bigint AS run_tokens
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN LATERAL (
    SELECT
        SUM(u.input_tokens + u.output_tokens)::bigint AS total_tokens,
        string_agg(DISTINCT u.model, ',' ORDER BY u.model) AS models
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

-- name: LatestQuizMeasuredScopeByWorkspace :one
-- Overview read path (RUYI-284): the agent scope whose quiz measurements are
-- most recent, so the overview serves that scope's real baseline comparison
-- instead of inventing a workspace-wide number. The agent join is the
-- workspace tenancy guard; the scope literal mirrors the sweep, whose
-- measurements are all agent-scoped.
SELECT r.scope, r.scope_id, a.name AS scope_name, max(r.measured_at)::timestamptz AS last_measured_at
FROM prompt_quiz_result r
JOIN agent a ON a.id = r.scope_id
WHERE r.scope = 'agent' AND a.workspace_id = $1
GROUP BY r.scope, r.scope_id, a.name
ORDER BY last_measured_at DESC
LIMIT 1;

-- name: GetPromptQuizTaskAnswer :one
-- The answer a quiz run produced: its last text message (RUYI-286 grading).
--
-- 'text' is the assistant-output message type the daemon writes (thinking /
-- tool_use / tool_result / error are the others); the LAST one by seq is the
-- run's conclusion. task_message is the single transcript — this reads it,
-- it does not copy it, so grading evidence can point at the run without the
-- result row growing a second copy of the answer.
--
-- ErrNoRows is the "no answer" case (errored run, or a completed run that
-- somehow produced no text): the collector maps it to a NULL score, which is
-- the "measured but not graded" state migration 950 defines.
SELECT content
FROM task_message
WHERE task_id = sqlc.arg('task_id')::uuid
  AND type = 'text'
  AND content IS NOT NULL
ORDER BY seq DESC
LIMIT 1;

-- name: ListPromptQuizResultsForBatch :many
-- One batch's rows for the traceability view (RUYI-286): per sample — which
-- item, which agent/version, outcome, score, and the task id that joins back
-- to the run. Owner-only at the route: it exposes score_detail's assertion
-- evidence, which is the private half's grading output.
--
-- LEFT JOIN, not JOIN: an item hard-deleted after the batch ran must not erase
-- the batch's history — the reading stays interpretable through item_revision
-- and item_body_sha256, which is exactly why the item is not a version table.
SELECT
    r.task_id,
    r.scope,
    r.scope_id,
    r.version,
    r.item_id,
    r.item_revision,
    r.outcome,
    r.score,
    r.score_detail,
    r.graded_at,
    r.measured_at,
    r.run_tokens,
    i.slug AS item_slug,
    i.title AS item_title,
    atq.status AS task_status
FROM prompt_quiz_result r
LEFT JOIN prompt_quiz_item i ON i.id = r.item_id AND i.workspace_id = r.workspace_id
JOIN agent_task_queue atq ON atq.id = r.task_id
WHERE r.workspace_id = sqlc.arg('workspace_id')::uuid
  AND r.batch_id = sqlc.arg('batch_id')::uuid
ORDER BY r.measured_at DESC
LIMIT sqlc.arg('row_limit')::int;

-- name: ListPromptQuizGradedSamples :many
-- One scope-version's graded samples, newest first, for the drill-down
-- (RUYI-286): the row the quality page reads to answer "what did this version
-- score on this question, when, and on what evidence". Owner-only at the
-- route for the same reason as the batch list above.
--
-- Like ListPromptQuizResultsForBatch: LEFT JOIN so deleting an item keeps its
-- readings readable; errored rows return with their outcome intact so "not
-- graded because the run errored" stays visible rather than collapsing into
-- the graded population.
SELECT
    r.task_id,
    r.batch_id,
    r.item_id,
    r.item_revision,
    r.item_body_sha256,
    r.outcome,
    r.score,
    r.score_detail,
    r.graded_at,
    r.measured_at,
    r.run_tokens,
    i.slug AS item_slug,
    i.title AS item_title
FROM prompt_quiz_result r
LEFT JOIN prompt_quiz_item i ON i.id = r.item_id AND i.workspace_id = r.workspace_id
WHERE r.workspace_id = sqlc.arg('workspace_id')::uuid
  AND r.scope = sqlc.arg('scope')::text
  AND r.scope_id = sqlc.arg('scope_id')::uuid
  AND r.version = sqlc.arg('version')::int
ORDER BY r.measured_at DESC
LIMIT sqlc.arg('row_limit')::int;
