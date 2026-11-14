-- name: LockTaskBudgetScope :exec
SELECT pg_advisory_xact_lock(hashtextextended('task-budget:' || @scope_id::text, 0));

-- name: SetTaskBudgetSnapshot :one
-- Merge only this owned key; preserve quick-create, review and recovery data.
UPDATE agent_task_queue
SET context = jsonb_set(COALESCE(context, '{}'::jsonb), '{budget_scope}', @snapshot::jsonb)
WHERE id = @id AND NOT COALESCE(context, '{}'::jsonb) ? 'budget_scope'
RETURNING *;

-- name: GetTaskBudgetUsage :one
-- Count reservations, including queued/cancelled runs, rather than only
-- executions. Token and cost limits are soft gates on subsequently reported
-- usage; running work is never advertised as having a hard monetary cap.
SELECT
    (SELECT COUNT(*) FROM agent_task_queue t WHERE t.context->'budget_scope'->>'scope_id' = @scope_id::text)::bigint AS runs,
    COALESCE(SUM(u.input_tokens::numeric + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens), 0)::text AS total_tokens,
    COALESCE(SUM(u.cost_usd_ticks::numeric), 0)::text AS cost_usd_ticks,
    EXISTS (
        SELECT 1 FROM agent_task_queue t
        WHERE t.context->'budget_scope'->>'scope_id' = @scope_id::text
          AND (t.started_at IS NOT NULL OR t.status IN ('completed', 'failed'))
          AND NOT EXISTS (SELECT 1 FROM task_usage x WHERE x.task_id = t.id)
    ) AS unknown_tokens,
    EXISTS (
        SELECT 1 FROM agent_task_queue t
        WHERE t.context->'budget_scope'->>'scope_id' = @scope_id::text
          AND (t.failure_reason IN ('budget_exhausted', 'budget_invalid')
            OR ((t.started_at IS NOT NULL OR t.status IN ('completed', 'failed')) AND
                (NOT EXISTS (SELECT 1 FROM task_usage x WHERE x.task_id = t.id)
                 OR EXISTS (SELECT 1 FROM task_usage x WHERE x.task_id = t.id AND x.cost_usd_ticks IS NULL))))
    ) AS unknown_cost,
    EXISTS (
        SELECT 1 FROM agent_task_queue t
        WHERE t.context->'budget_scope'->>'scope_id' = @scope_id::text
          AND t.failure_reason IN ('budget_exhausted', 'budget_invalid')
    ) AS exhausted
FROM task_usage u JOIN agent_task_queue t ON t.id = u.task_id
WHERE t.context->'budget_scope'->>'scope_id' = @scope_id::text;

-- name: MarkTaskBudgetExhausted :one
-- Keep the original output, session and recovery/workdir pointers intact.
UPDATE agent_task_queue
SET failure_reason = 'budget_exhausted',
    error = concat_ws(E'\n', NULLIF(error, ''), @budget_error::text),
    fire_at = NULL
WHERE id = @id AND status = 'failed'
RETURNING *;
