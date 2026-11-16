-- RUYI-940: task_usage rows for ACP session-resume providers used to store the
-- provider's cumulative session snapshot.  Issue totals sum task_usage rows,
-- so those historical snapshots must be converted to per-task deltas before
-- the claim-time baseline query can safely sum them.
--
-- Version 1 is the historical snapshot shape. Version 2 is the canonical
-- per-task delta shape used by the daemon and all later writes.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS usage_semantics_version SMALLINT NOT NULL DEFAULT 1;

WITH ordered AS (
    SELECT
        tu.id,
        tu.input_tokens,
        tu.output_tokens,
        tu.cache_read_tokens,
        tu.cache_write_tokens,
        tu.cost_usd_ticks,
        LAG(tu.input_tokens) OVER w AS prior_input_tokens,
        LAG(tu.output_tokens) OVER w AS prior_output_tokens,
        LAG(tu.cache_read_tokens) OVER w AS prior_cache_read_tokens,
        LAG(tu.cache_write_tokens) OVER w AS prior_cache_write_tokens,
        LAG(tu.cost_usd_ticks) OVER w AS prior_cost_usd_ticks
    FROM task_usage tu
    JOIN agent_task_queue atq ON atq.id = tu.task_id
    WHERE tu.usage_semantics_version = 1
      AND atq.session_id IS NOT NULL
      AND lower(tu.provider) IN (
          'claude', 'hermes', 'kimi', 'kiro', 'qoder', 'qoderclicn',
          'traecli', 'grok', 'qwenpaw', 'mcode', 'dim', 'zeroclaw',
          'deerflow', 'reasonix', 'zcode'
      )
    WINDOW w AS (
        PARTITION BY atq.agent_id, atq.session_id, lower(tu.provider), tu.model
        ORDER BY
            COALESCE(atq.completed_at, atq.started_at, atq.created_at),
            tu.updated_at,
            tu.created_at,
            tu.id
    )
), normalized AS (
    SELECT
        id,
        CASE
            WHEN prior_input_tokens IS NULL THEN input_tokens
            ELSE GREATEST(input_tokens - prior_input_tokens, 0)
        END AS input_tokens,
        CASE
            WHEN prior_output_tokens IS NULL THEN output_tokens
            ELSE GREATEST(output_tokens - prior_output_tokens, 0)
        END AS output_tokens,
        CASE
            WHEN prior_cache_read_tokens IS NULL THEN cache_read_tokens
            ELSE GREATEST(cache_read_tokens - prior_cache_read_tokens, 0)
        END AS cache_read_tokens,
        CASE
            WHEN prior_cache_write_tokens IS NULL THEN cache_write_tokens
            ELSE GREATEST(cache_write_tokens - prior_cache_write_tokens, 0)
        END AS cache_write_tokens,
        CASE
            WHEN cost_usd_ticks IS NULL THEN NULL::bigint
            WHEN prior_cost_usd_ticks IS NULL THEN cost_usd_ticks
            ELSE GREATEST(cost_usd_ticks - prior_cost_usd_ticks, 0)
        END AS cost_usd_ticks
    FROM ordered
)
UPDATE task_usage tu
SET input_tokens = n.input_tokens,
    output_tokens = n.output_tokens,
    cache_read_tokens = n.cache_read_tokens,
    cache_write_tokens = n.cache_write_tokens,
    cost_usd_ticks = n.cost_usd_ticks,
    usage_semantics_version = 2,
    updated_at = now()
FROM normalized n
WHERE tu.id = n.id;

UPDATE task_usage
SET usage_semantics_version = 2,
    updated_at = now()
WHERE usage_semantics_version = 1;
