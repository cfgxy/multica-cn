-- Bootstrap the hourly watermark for databases that only started collecting
-- task usage after the hourly schema was installed.
--
-- The default 1970 watermark is correct for an upgrade with pre-hourly
-- history: taskusagebackfill.Hook must walk that history before the regular
-- one-day scheduler window may advance it. It is pathological for a fresh
-- install whose migration hook was skipped, though: every tick advances only
-- one day from 1970 and never reaches recent usage in a useful timeframe.
--
-- A task_usage row created before the hourly schema proves that the database
-- has historical usage. Keep the epoch watermark in that case so the explicit
-- backfill remains the only path that declares the history complete. Otherwise
-- begin at the earliest post-schema event. This preserves every recorded row;
-- bounded regular ticks can catch up without a multi-decade empty scan.
DO $$
DECLARE
    v_hourly_schema_applied_at TIMESTAMPTZ;
    v_first_event             TIMESTAMPTZ;
BEGIN
    SELECT applied_at
      INTO v_hourly_schema_applied_at
      FROM schema_migrations
     WHERE version = '101_task_usage_hourly_schema';

    IF v_hourly_schema_applied_at IS NULL THEN
        RAISE EXCEPTION
            'task_usage hourly watermark bootstrap requires migration 101_task_usage_hourly_schema in schema_migrations';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM task_usage
         WHERE created_at < v_hourly_schema_applied_at
    ) THEN
        RETURN;
    END IF;

    SELECT LEAST(
        now() - INTERVAL '5 minutes',
        MIN(COALESCE(updated_at, created_at))
    )
      INTO v_first_event
      FROM task_usage;

    UPDATE task_usage_hourly_rollup_state
       SET watermark_at = COALESCE(v_first_event, now() - INTERVAL '5 minutes')
     WHERE id = 1
       AND watermark_at = '1970-01-01 00:00:00+00';
END
$$;
