-- Daily rollup table for `task_usage`. Background: the dashboard query
-- ListRuntimeUsage runs `SUM() GROUP BY DATE(created_at), provider, model`
-- against the raw event stream and is called once per runtime row on the
-- runtimes list (plus once per detail page load), so it dominates DB load
-- as event volume grows. We materialise the day-bucketed aggregate here
-- so reads scan O(days × providers × models) rows instead of O(events).
--
-- All query dimensions are denormalised into the table so reads never
-- need to join `agent_task_queue`. The PK doubles as the upsert key for
-- the rollup worker, which makes late-arriving events idempotent.
CREATE TABLE task_usage_daily (
    bucket_date         DATE        NOT NULL,
    workspace_id        UUID        NOT NULL,
    runtime_id          UUID        NOT NULL,
    provider            TEXT        NOT NULL,
    model               TEXT        NOT NULL,
    input_tokens        BIGINT      NOT NULL DEFAULT 0,
    output_tokens       BIGINT      NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT      NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT      NOT NULL DEFAULT 0,
    event_count         BIGINT      NOT NULL DEFAULT 0,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_date, workspace_id, runtime_id, provider, model)
);

-- Primary read path: runtime detail page + runtimes-list cost cell, both
-- filter by runtime_id and order by date DESC. bucket_date DESC in the
-- index lets the query avoid an extra sort.
CREATE INDEX idx_task_usage_daily_runtime_date
    ON task_usage_daily (runtime_id, bucket_date DESC);

-- Workspace-wide aggregations (e.g. future per-workspace cost dashboards
-- or the batched list-page query GPT-Boy's earlier PR sketched out) hit
-- this index instead of fanning out per-runtime.
CREATE INDEX idx_task_usage_daily_workspace_date
    ON task_usage_daily (workspace_id, bucket_date DESC);

-- The rollup worker scans `task_usage` by created_at window. The same
-- index also accelerates the two remaining lazy queries
-- (ListRuntimeUsageByAgent / GetRuntimeUsageByHour) that still hit the
-- raw table — they currently rely on filtering by runtime_id then
-- created_at via the agent_task_queue join.
CREATE INDEX IF NOT EXISTS idx_task_usage_created_at
    ON task_usage (created_at);

-- Single-row state table tracking how far the rollup worker has consumed.
-- Singleton enforced via id=1 CHECK so concurrent inserts can't create a
-- second row.
CREATE TABLE task_usage_rollup_state (
    id                    SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    watermark_at          TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    last_run_started_at   TIMESTAMPTZ,
    last_run_finished_at  TIMESTAMPTZ,
    last_run_rows         BIGINT      NOT NULL DEFAULT 0,
    last_error            TEXT
);
INSERT INTO task_usage_rollup_state (id) VALUES (1) ON CONFLICT DO NOTHING;

-- Window-based aggregation primitive. Used by both the cron-driven
-- watermark advancer and the offline backfill command, so they stay
-- byte-identical in their aggregation semantics. Returns the number of
-- output rows touched.
--
-- Late-arriving events into an already-rolled-up bucket are handled by
-- the ON CONFLICT DO UPDATE: each invocation contributes its delta on
-- top of whatever's already there. That means callers MUST NOT pass
-- overlapping windows for the same data, or rows will double-count.
-- The watermark wrapper below guarantees non-overlap by construction.
CREATE OR REPLACE FUNCTION rollup_task_usage_daily_window(
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

    WITH agg AS (
        SELECT
            DATE(tu.created_at)                               AS bucket_date,
            i.workspace_id                                    AS workspace_id,
            atq.runtime_id                                    AS runtime_id,
            tu.provider                                       AS provider,
            tu.model                                          AS model,
            SUM(tu.input_tokens)::bigint                      AS input_tokens,
            SUM(tu.output_tokens)::bigint                     AS output_tokens,
            SUM(tu.cache_read_tokens)::bigint                 AS cache_read_tokens,
            SUM(tu.cache_write_tokens)::bigint                AS cache_write_tokens,
            COUNT(*)::bigint                                  AS event_count
        FROM task_usage tu
        JOIN agent_task_queue atq ON atq.id = tu.task_id
        JOIN issue i              ON i.id   = atq.issue_id
        WHERE tu.created_at >= p_from
          AND tu.created_at <  p_to
          AND atq.runtime_id IS NOT NULL
        GROUP BY 1, 2, 3, 4, 5
    )
    INSERT INTO task_usage_daily AS d (
        bucket_date, workspace_id, runtime_id, provider, model,
        input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
        event_count
    )
    SELECT
        bucket_date, workspace_id, runtime_id, provider, model,
        input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
        event_count
    FROM agg
    ON CONFLICT (bucket_date, workspace_id, runtime_id, provider, model) DO UPDATE
        SET input_tokens       = d.input_tokens       + EXCLUDED.input_tokens,
            output_tokens      = d.output_tokens      + EXCLUDED.output_tokens,
            cache_read_tokens  = d.cache_read_tokens  + EXCLUDED.cache_read_tokens,
            cache_write_tokens = d.cache_write_tokens + EXCLUDED.cache_write_tokens,
            event_count        = d.event_count        + EXCLUDED.event_count,
            updated_at         = now();

    GET DIAGNOSTICS v_rows = ROW_COUNT;
    RETURN v_rows;
END;
$$;

-- Cron entry point. Advances the watermark by one window each call.
--
-- Invariants:
--  * `pg_try_advisory_lock(4242)` serialises overlapping ticks. If the
--    previous tick is still running we skip this one rather than queue;
--    catching up happens naturally on the next tick.
--  * The window upper bound is `now() - 5 minutes`. The lag exists
--    because `task_usage` rows are written from a separate transaction;
--    a row with created_at = T can become visible to this snapshot at
--    some t > T. 5 minutes is a generous bound on that visibility delay
--    and keeps the dashboard "today" bucket at most ~10 min stale
--    (5 min lag + 5 min cron period), which is well below human-noticeable.
--  * On error we record `last_error` and re-raise so the cron framework
--    surfaces the failure; the watermark is NOT advanced because the
--    UPDATE that advances it only runs after the upsert succeeds.
CREATE OR REPLACE FUNCTION rollup_task_usage_daily()
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
    v_lock_ok BOOLEAN;
    v_from    TIMESTAMPTZ;
    v_to      TIMESTAMPTZ;
    v_rows    BIGINT := 0;
BEGIN
    SELECT pg_try_advisory_lock(4242) INTO v_lock_ok;
    IF NOT v_lock_ok THEN
        RETURN 0;
    END IF;

    BEGIN
        UPDATE task_usage_rollup_state
           SET last_run_started_at = now(),
               last_error          = NULL
         WHERE id = 1
        RETURNING watermark_at INTO v_from;

        v_to := now() - INTERVAL '5 minutes';

        IF v_from < v_to THEN
            v_rows := rollup_task_usage_daily_window(v_from, v_to);

            UPDATE task_usage_rollup_state
               SET watermark_at         = v_to,
                   last_run_finished_at = now(),
                   last_run_rows        = v_rows
             WHERE id = 1;
        ELSE
            UPDATE task_usage_rollup_state
               SET last_run_finished_at = now(),
                   last_run_rows        = 0
             WHERE id = 1;
        END IF;

        PERFORM pg_advisory_unlock(4242);
        RETURN v_rows;
    EXCEPTION WHEN OTHERS THEN
        UPDATE task_usage_rollup_state
           SET last_error           = SQLERRM,
               last_run_finished_at = now()
         WHERE id = 1;
        PERFORM pg_advisory_unlock(4242);
        RAISE;
    END;
END;
$$;
