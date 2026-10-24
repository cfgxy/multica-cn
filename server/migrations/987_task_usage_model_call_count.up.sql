-- Model API/Prompt call counts, alongside the token/cost counters.
--
-- WHY: the pipeline has always counted tokens, cost, tasks and events — never
-- how many provider calls a run made. `multica_llm_request_total` is not a
-- substitute: it Inc()s once per aggregated usage row on the write path, i.e.
-- it counts daemon reports, not provider requests. Call counts must come from
-- provider-original evidence the harness observed (per-call events, or a
-- provider-reported cumulative counter), deduplicated per task.
--
-- NULL is the convention (same as cost_usd_ticks, migration 213, and the
-- 9xx observation columns, migration 906): NULL means "the provider did not
-- report a call count", never zero. A run that genuinely made zero model
-- calls writes no task_usage row at all, so a known 0 is not representable in
-- practice and a NULL display must not render as "0". No backfill: historical
-- rows have no provider call evidence to recover; inventing one would be a
-- guess.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS model_call_count BIGINT;

COMMENT ON COLUMN task_usage.model_call_count IS
    'Provider-reported or provider-observed model API call count for this task, e.g. xAI modelCalls / a per-call usage event stream. NULL when the provider does not report it; history before this column is permanently NULL.';

-- Rollup side. One hourly bucket can mix rows that carry a call count with
-- rows that do not (two providers, or a CLI upgrade inside the hour). Summing
-- only the known half and presenting it as the bucket total would silently
-- under-report, so the rollup carries both halves:
--
--   model_call_count         - sum over rows that HAVE a call count
--   model_call_unknown_count - rows that do NOT, i.e. the count of buckets'
--                              inputs still without provider call evidence
--
-- Consumers report `known total` together with `N rows unknown`, and must not
-- present the known half as a complete total when unknown_count > 0.
--
-- `model_call_unknown_count` is NOT NULL DEFAULT 0: 0 is the honest identity
-- for "every row here had a call count", which is true of every bucket
-- recomputed after this migration, and of historical buckets that had none of
-- the new column at all.
--
-- All additive, so the unique key, the dirty-queue key shape, and the
-- triggers are unaffected.

ALTER TABLE task_usage_hourly
    ADD COLUMN IF NOT EXISTS model_call_count       BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS model_call_unknown_count BIGINT NOT NULL DEFAULT 0;

-- Existing buckets predate provider call evidence. Preserve their event count
-- as unknown inputs instead of presenting the new default zeros as known data.
UPDATE task_usage_hourly
   SET model_call_unknown_count = event_count
 WHERE event_count > 0;

COMMENT ON COLUMN task_usage_hourly.model_call_count IS
    'Sum of model_call_count over the rows in this bucket that had one; 0 when none did.';
COMMENT ON COLUMN task_usage_hourly.model_call_unknown_count IS
    'Rows in this bucket whose model_call_count is NULL (provider did not report it). Readers must not present model_call_count as a complete total while this is nonzero.';

-- Teach the rollup to populate them. Body is migration 213's, with the two new
-- expression groups in `recomputed` and the matching SET list on conflict;
-- everything else — dirty discovery, the empty-bucket delete, the queue drain,
-- the idempotency contract — is unchanged.
CREATE OR REPLACE FUNCTION rollup_task_usage_hourly_window(
    p_from TIMESTAMPTZ,
    p_to   TIMESTAMPTZ
)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
    v_rows BIGINT;
BEGIN
    IF p_from >= p_to THEN
        RETURN 0;
    END IF;

    WITH
    dirty_from_updates AS (
        SELECT DISTINCT
            task_usage_hour_bucket(tu.created_at) AS bucket_hour,
            a.workspace_id                        AS workspace_id,
            atq.runtime_id                        AS runtime_id,
            atq.agent_id                          AS agent_id,
            i.project_id                          AS project_id,
            tu.provider                           AS provider,
            tu.model                              AS model
          FROM task_usage tu
          JOIN agent_task_queue atq ON atq.id      = tu.task_id
          JOIN agent            a   ON a.id        = atq.agent_id
          LEFT JOIN issue       i   ON i.id        = atq.issue_id
         WHERE atq.runtime_id IS NOT NULL
           AND (
                (tu.updated_at >= p_from AND tu.updated_at < p_to)
                -- Legacy updated_at-NULL rows; partial index from 078.
                OR (tu.updated_at IS NULL
                    AND tu.created_at >= p_from
                    AND tu.created_at <  p_to)
           )
    ),
    dirty_from_queue AS (
        SELECT bucket_hour, workspace_id, runtime_id, agent_id,
               project_id, provider, model
          FROM task_usage_hourly_dirty
         WHERE enqueued_at < p_to
    ),
    dirty_keys AS (
        SELECT * FROM dirty_from_updates
        UNION
        SELECT * FROM dirty_from_queue
    ),
    recomputed AS (
        SELECT
            dk.bucket_hour,
            dk.workspace_id,
            dk.runtime_id,
            dk.agent_id,
            dk.project_id,
            dk.provider,
            dk.model,
            SUM(tu.input_tokens)::bigint       AS input_tokens,
            SUM(tu.output_tokens)::bigint      AS output_tokens,
            SUM(tu.cache_read_tokens)::bigint  AS cache_read_tokens,
            SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
            -- Authoritative half: only rows the provider priced.
            COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
            -- Estimated half: tokens from rows the provider did not price.
            COALESCE(SUM(tu.input_tokens)       FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_input_tokens,
            COALESCE(SUM(tu.output_tokens)      FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_output_tokens,
            COALESCE(SUM(tu.cache_read_tokens)  FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_read_tokens,
            COALESCE(SUM(tu.cache_write_tokens) FILTER (WHERE tu.cost_usd_ticks IS NULL), 0)::bigint AS uncosted_cache_write_tokens,
            -- Call-count half: rows with provider call evidence.
            COALESCE(SUM(tu.model_call_count) FILTER (WHERE tu.model_call_count IS NOT NULL), 0)::bigint AS model_call_count,
            COUNT(*) FILTER (WHERE tu.model_call_count IS NULL)::bigint AS model_call_unknown_count,
            COUNT(DISTINCT tu.task_id)::bigint AS task_count,
            COUNT(*)::bigint                   AS event_count
          FROM dirty_keys dk
          JOIN agent_task_queue atq ON atq.runtime_id  = dk.runtime_id
                                    AND atq.agent_id    = dk.agent_id
          JOIN agent            a   ON a.id            = atq.agent_id
                                    AND a.workspace_id = dk.workspace_id
          LEFT JOIN issue       i   ON i.id            = atq.issue_id
          JOIN task_usage       tu  ON tu.task_id      = atq.id
                                    AND tu.provider    = dk.provider
                                    AND tu.model       = dk.model
                                    AND task_usage_hour_bucket(tu.created_at) = dk.bucket_hour
         WHERE (i.project_id IS NOT DISTINCT FROM dk.project_id)
         GROUP BY 1, 2, 3, 4, 5, 6, 7
    ),
    upserted AS (
        INSERT INTO task_usage_hourly AS d (
            bucket_hour, workspace_id, runtime_id, agent_id,
            project_id, provider, model,
            input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
            cost_usd_ticks,
            uncosted_input_tokens, uncosted_output_tokens,
            uncosted_cache_read_tokens, uncosted_cache_write_tokens,
            model_call_count, model_call_unknown_count,
            task_count, event_count
        )
        SELECT
            bucket_hour, workspace_id, runtime_id, agent_id,
            project_id, provider, model,
            input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
            cost_usd_ticks,
            uncosted_input_tokens, uncosted_output_tokens,
            uncosted_cache_read_tokens, uncosted_cache_write_tokens,
            model_call_count, model_call_unknown_count,
            task_count, event_count
          FROM recomputed
        ON CONFLICT ON CONSTRAINT uq_task_usage_hourly_key DO UPDATE
            SET input_tokens                = EXCLUDED.input_tokens,
                output_tokens               = EXCLUDED.output_tokens,
                cache_read_tokens           = EXCLUDED.cache_read_tokens,
                cache_write_tokens          = EXCLUDED.cache_write_tokens,
                cost_usd_ticks             = EXCLUDED.cost_usd_ticks,
                uncosted_input_tokens      = EXCLUDED.uncosted_input_tokens,
                uncosted_output_tokens     = EXCLUDED.uncosted_output_tokens,
                uncosted_cache_read_tokens = EXCLUDED.uncosted_cache_read_tokens,
                uncosted_cache_write_tokens = EXCLUDED.uncosted_cache_write_tokens,
                model_call_count           = EXCLUDED.model_call_count,
                model_call_unknown_count   = EXCLUDED.model_call_unknown_count,
                task_count                 = EXCLUDED.task_count,
                event_count                = EXCLUDED.event_count,
                updated_at                 = now()
        RETURNING 1
    ),
    deleted_empty AS (
        DELETE FROM task_usage_hourly d
         USING dirty_keys dk
         WHERE d.bucket_hour  = dk.bucket_hour
           AND d.workspace_id = dk.workspace_id
           AND d.runtime_id   = dk.runtime_id
           AND d.agent_id     = dk.agent_id
           AND d.project_id IS NOT DISTINCT FROM dk.project_id
           AND d.provider     = dk.provider
           AND d.model        = dk.model
           AND NOT EXISTS (
               SELECT 1 FROM recomputed r
                WHERE r.bucket_hour  = dk.bucket_hour
                  AND r.workspace_id = dk.workspace_id
                  AND r.runtime_id   = dk.runtime_id
                  AND r.agent_id     = dk.agent_id
                  AND r.project_id IS NOT DISTINCT FROM dk.project_id
                  AND r.provider     = dk.provider
                  AND r.model        = dk.model
           )
        RETURNING 1
    )
    SELECT (SELECT COUNT(*) FROM upserted) + (SELECT COUNT(*) FROM deleted_empty)
      INTO v_rows;

    DELETE FROM task_usage_hourly_dirty WHERE enqueued_at < p_to;

    RETURN v_rows;
END;
$$;
