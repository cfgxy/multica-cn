-- The pre-attribution schema can only store one row per task/provider/model.
-- Collapse session-split rows before restoring that unique constraint so a
-- disposable rollback remains executable without dropping the usage total.
CREATE TEMP TABLE task_usage_session_rollback ON COMMIT DROP AS
SELECT
    (array_agg(id ORDER BY created_at, id))[1] AS keeper_id,
    task_id,
    provider,
    model,
    SUM(input_tokens)::bigint AS input_tokens,
    SUM(output_tokens)::bigint AS output_tokens,
    SUM(cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(cache_write_tokens)::bigint AS cache_write_tokens,
    SUM(cost_usd_ticks)::bigint AS cost_usd_ticks,
    MAX(context_tokens)::bigint AS context_tokens,
    MAX(turns)::integer AS turns,
    MAX(compactions)::integer AS compactions,
    MAX(max_context_tokens)::bigint AS max_context_tokens
FROM task_usage
GROUP BY task_id, provider, model;

DELETE FROM task_usage tu
USING task_usage_session_rollback r
WHERE tu.task_id = r.task_id
  AND tu.provider = r.provider
  AND tu.model = r.model
  AND tu.id <> r.keeper_id;

UPDATE task_usage tu
SET input_tokens = r.input_tokens,
    output_tokens = r.output_tokens,
    cache_read_tokens = r.cache_read_tokens,
    cache_write_tokens = r.cache_write_tokens,
    cost_usd_ticks = r.cost_usd_ticks,
    context_tokens = r.context_tokens,
    turns = r.turns,
    compactions = r.compactions,
    max_context_tokens = r.max_context_tokens
FROM task_usage_session_rollback r
WHERE tu.id = r.keeper_id;

ALTER TABLE task_usage
    ADD CONSTRAINT task_usage_task_id_provider_model_key
    UNIQUE (task_id, provider, model);

ALTER TABLE task_usage
    DROP COLUMN IF EXISTS session_id;
