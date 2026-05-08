-- Schedule rollup_task_usage_daily() every 5 minutes via pg_cron.
--
-- pg_cron requires `shared_preload_libraries = 'pg_cron'` at server
-- start, which staging/prod set but local/CI generally don't. We skip
-- gracefully when the extension can't be created — same pattern as
-- 032_issue_search_index for pg_bigm — so this migration applies
-- everywhere. In environments without pg_cron, ops can either:
--   (a) install pg_cron and re-run the schedule statement manually, or
--   (b) drive the rollup from an application-side ticker that calls
--       SELECT rollup_task_usage_daily() on the same cadence.
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_cron;
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'pg_cron not available; rollup_task_usage_daily() will not be auto-scheduled';
END
$$;

DO $$
BEGIN
    -- Only schedule when pg_cron is actually present.
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
        -- Defensive: drop any prior schedule with the same name so the
        -- migration is re-runnable after manual edits.
        PERFORM cron.unschedule('rollup_task_usage_daily')
        WHERE EXISTS (
            SELECT 1 FROM cron.job WHERE jobname = 'rollup_task_usage_daily'
        );

        PERFORM cron.schedule(
            'rollup_task_usage_daily',
            '*/5 * * * *',
            'SELECT rollup_task_usage_daily();'
        );
    END IF;
END
$$;
