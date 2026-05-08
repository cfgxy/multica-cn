DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
        PERFORM cron.unschedule('rollup_task_usage_daily')
        WHERE EXISTS (
            SELECT 1 FROM cron.job WHERE jobname = 'rollup_task_usage_daily'
        );
    END IF;
END
$$;
