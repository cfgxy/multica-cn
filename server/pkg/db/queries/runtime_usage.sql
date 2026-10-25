-- name: ListRuntimeUsage :many
-- Reads from the UTC-bucketed `task_usage_hourly` rollup table,
-- aggregated to per-(date, provider, model) under the
-- caller-supplied @tz. Powers the trend chart on the runtime detail
-- page and the per-row cost cell on the runtimes list.
--
-- @tz is required, even if the caller intends "UTC", so the bucket
-- cast is unambiguous — `bucket_hour` is UTC and the caller picks the
-- calendar boundary per request.
--
-- provider is LOWER()-normalized so mixed-case historical rows merge
-- (same reason as ListRuntimeUsageByAgent below).
SELECT
    DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text) AS date,
    LOWER(provider) AS provider,
    model,
    SUM(input_tokens)::bigint        AS input_tokens,
    SUM(output_tokens)::bigint       AS output_tokens,
    SUM(cache_read_tokens)::bigint   AS cache_read_tokens,
    SUM(cache_write_tokens)::bigint  AS cache_write_tokens,
    SUM(cost_usd_ticks)::bigint                                          AS cost_usd_ticks,
    SUM(COALESCE(uncosted_input_tokens, input_tokens))::bigint           AS uncosted_input_tokens,
    SUM(COALESCE(uncosted_output_tokens, output_tokens))::bigint         AS uncosted_output_tokens,
    SUM(COALESCE(uncosted_cache_read_tokens, cache_read_tokens))::bigint AS uncosted_cache_read_tokens,
    SUM(COALESCE(uncosted_cache_write_tokens, cache_write_tokens))::bigint AS uncosted_cache_write_tokens,
    SUM(model_call_count)::bigint         AS model_call_count,
    SUM(model_call_unknown_count)::bigint AS model_call_unknown_count
FROM task_usage_hourly
WHERE runtime_id = $1
  AND bucket_hour >= sqlc.arg('since')::timestamptz
GROUP BY DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text), LOWER(provider), model
ORDER BY DATE(bucket_hour AT TIME ZONE sqlc.arg('tz')::text) DESC, LOWER(provider), model;

-- name: GetRuntimeModelCallBudgetUsage :one
-- Rolling, raw usage for one Runtime's provider-call admission budget. Rows
-- with NULL model_call_count are intentionally counted separately: a partial
-- known sum must not be treated as the whole window and therefore cannot hold
-- a Runtime.
SELECT
    COALESCE((
        SELECT COUNT(*)
        FROM task_usage_model_call_evidence e
        JOIN task_usage tu ON tu.task_id = e.task_id
            AND tu.provider = e.provider AND tu.model = e.model
        JOIN agent_task_queue atq ON atq.id = e.task_id
        WHERE atq.runtime_id = @runtime_id
          AND tu.model_call_count IS NOT NULL
          AND e.created_at >= @since::timestamptz
    ), 0)::bigint AS known_calls,
    COALESCE((
        SELECT COUNT(*)
        FROM task_usage tu
        JOIN agent_task_queue atq ON atq.id = tu.task_id
        WHERE atq.runtime_id = @runtime_id
          AND tu.model_call_count IS NULL
          AND tu.created_at >= @since::timestamptz
    ), 0)::bigint AS unknown_usage_rows;

-- name: ListRuntimeModelCallBudgetUsage :many
-- Batch equivalent for queued-reason decoration and multi-runtime claims. The
-- time predicate derives its rolling boundary from each runtime's persisted
-- config, keeping a mixed set of windows correct in one round trip.
WITH runtime_windows AS (
    SELECT id AS runtime_id,
           to_timestamp(EXTRACT(EPOCH FROM @now::timestamptz) - model_call_budget_window_seconds) AS since
    FROM agent_runtime
    WHERE id = ANY(@runtime_ids::uuid[])
), known_counts AS (
    SELECT rw.runtime_id, COUNT(*)::bigint AS known_calls
    FROM runtime_windows rw
    JOIN agent_task_queue atq ON atq.runtime_id = rw.runtime_id
    JOIN task_usage_model_call_evidence e ON e.task_id = atq.id AND e.created_at >= rw.since
    JOIN task_usage tu ON tu.task_id = e.task_id
        AND tu.provider = e.provider AND tu.model = e.model
        AND tu.model_call_count IS NOT NULL
    GROUP BY rw.runtime_id
), unknown_counts AS (
    SELECT rw.runtime_id, COUNT(*)::bigint AS unknown_usage_rows
    FROM runtime_windows rw
    JOIN agent_task_queue atq ON atq.runtime_id = rw.runtime_id
    JOIN task_usage tu ON tu.task_id = atq.id
        AND tu.model_call_count IS NULL AND tu.created_at >= rw.since
    GROUP BY rw.runtime_id
)
SELECT rw.runtime_id,
       COALESCE(kc.known_calls, 0)::bigint AS known_calls,
       COALESCE(uc.unknown_usage_rows, 0)::bigint AS unknown_usage_rows
FROM runtime_windows rw
LEFT JOIN known_counts kc ON kc.runtime_id = rw.runtime_id
LEFT JOIN unknown_counts uc ON uc.runtime_id = rw.runtime_id;

-- name: GetRuntimeTaskHourlyActivity :many
-- Hour-of-day distribution for queue starts. Bucketed in the viewer's
-- tz so "this runtime is busy in the afternoon" actually means
-- the operator's afternoon, not UTC's.
SELECT EXTRACT(HOUR FROM started_at AT TIME ZONE @tz::text)::int AS hour,
       COUNT(*)::int AS count
FROM agent_task_queue
WHERE runtime_id = $1 AND started_at IS NOT NULL
GROUP BY hour
ORDER BY hour;

-- name: ListRuntimeUsageByAgent :many
-- Per-(agent, provider, model) token aggregates for a runtime since a cutoff. Powers
-- the runtime-detail "Cost by agent" tab. task_usage only carries task_id,
-- so we join the queue to expose agent_id. The model dimension is kept on
-- purpose: cost is computed client-side from a per-model pricing table, so
-- collapsing models server-side would erase the information needed to do
-- that arithmetic. The client groups by agent_id and sums cost per agent.
--
-- This view doesn't bucket by date, so it doesn't need @tz; only the
-- @since cutoff is provided in runtime-local terms (computed in Go).
-- provider is LOWER()-normalized so mixed-case historical rows merge with
-- new rows (see ListDashboardUsageDaily in task_usage.sql).
SELECT
    atq.agent_id,
    LOWER(tu.provider) AS provider,
    tu.model,
    SUM(tu.input_tokens)::bigint AS input_tokens,
    SUM(tu.output_tokens)::bigint AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens,
    -- Call counts straight off the raw rows (this query has no hourly
    -- rollup available): known half only, plus the row count without
    -- provider evidence so readers cannot pad a partial sum to "complete".
    COALESCE(SUM(tu.model_call_count) FILTER (WHERE tu.model_call_count IS NOT NULL), 0)::bigint AS model_call_count,
    COUNT(*) FILTER (WHERE tu.model_call_count IS NULL)::int  AS model_call_unknown_count,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
WHERE atq.runtime_id = $1
  AND tu.created_at >= @since::timestamptz
GROUP BY atq.agent_id, LOWER(tu.provider), tu.model
ORDER BY atq.agent_id, LOWER(tu.provider), tu.model;

-- name: GetRuntimeUsageByHour :many
-- Per-(hour, model) token aggregates (hour ∈ 0..23) for a runtime since a
-- cutoff. Powers the "By hour" tab — shows when in the day this runtime is
-- doing real work, with model preserved for client-side cost calculation
-- (same reason as ListRuntimeUsageByAgent above). Hours with zero activity
-- are omitted; the client fills the 24-bucket axis.
--
-- Hours are extracted in the viewer's tz via @tz so afternoon
-- work bucketed at UTC 06:00 lands in 14:00 for a UTC+8 viewer.
SELECT
    EXTRACT(HOUR FROM tu.created_at AT TIME ZONE @tz::text)::int AS hour,
    tu.model,
    SUM(tu.input_tokens)::bigint AS input_tokens,
    SUM(tu.output_tokens)::bigint AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
    COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
    COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens,
    COALESCE(SUM(tu.model_call_count) FILTER (WHERE tu.model_call_count IS NOT NULL), 0)::bigint AS model_call_count,
    COUNT(*) FILTER (WHERE tu.model_call_count IS NULL)::int  AS model_call_unknown_count,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
WHERE atq.runtime_id = $1
  AND tu.created_at >= @since::timestamptz
GROUP BY EXTRACT(HOUR FROM tu.created_at AT TIME ZONE @tz::text), tu.model
ORDER BY hour, tu.model;
