-- Drop the two legacy daily rollup pipelines now that `task_usage_hourly`
-- is the only read path (see docs/timezone-architecture-rfc.md §6,
-- Phase 3). Forward-only: there is no down migration that would put the
-- data back, since by the time this ships:
--
--   * The hourly rollup has been live and writing every bucket since
--     the hourly-pipeline migration.
--   * No handler reads from `task_usage_daily` or
--     `task_usage_dashboard_daily`. The runtime PATCH path that used
--     to delete/insert into `task_usage_daily` on tz change has been
--     replaced by a single UPDATE.
--   * The `cmd/backfill_task_usage_daily` and
--     `cmd/backfill_task_usage_dashboard_daily` commands have been
--     removed. The remaining `cmd/backfill_task_usage_hourly` is the
--     only seed path going forward.
--
-- The two pg_cron entries are unscheduled below before their functions
-- are dropped, so a still-registered job cannot tick into a
-- `function does not exist` error. The cron.unschedule calls are wrapped
-- so the migration still succeeds on instances without pg_cron at all
-- (same guard pattern as migration 076).

-- ---------------------------------------------------------------------------
-- Fail-closed guard: refuse to drop the legacy daily pipelines while the
-- new hourly rollup is empty on a database that already has token-event
-- history. `migrate up` runs the migration set straight through (no
-- per-version stop), so a self-host operator who skips the documented
-- backfill step would otherwise silently land in a state where dashboards
-- show zeros (see SELF-HOST UPGRADE ORDER in
-- cmd/backfill_task_usage_hourly/main.go and
-- docs/timezone-architecture-rfc.md §6 / §7.1). Failing loud here is the
-- only thing that turns an undetected outage into a clear migration error.
--
-- A fresh database (no rows in task_usage) is exempt — there is nothing to
-- backfill, and the new triggers installed in 102 will populate
-- task_usage_hourly on the first event.
-- ---------------------------------------------------------------------------

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM task_usage LIMIT 1)
       AND NOT EXISTS (SELECT 1 FROM task_usage_hourly LIMIT 1) THEN
        RAISE EXCEPTION
            'refusing to drop legacy daily rollups: task_usage_hourly is empty but task_usage has data — apply migrations 100-102 first, then run cmd/backfill_task_usage_hourly, then re-run migrate (see SELF-HOST UPGRADE ORDER in cmd/backfill_task_usage_hourly/main.go)';
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- Unschedule the legacy pg_cron jobs first (no-op when pg_cron is absent
-- or the job was never registered).
-- ---------------------------------------------------------------------------

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
        PERFORM cron.unschedule('rollup_task_usage_daily')
          FROM cron.job WHERE jobname = 'rollup_task_usage_daily';
        PERFORM cron.unschedule('rollup_task_usage_dashboard_daily')
          FROM cron.job WHERE jobname = 'rollup_task_usage_dashboard_daily';
    END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- task_usage_dashboard_daily pipeline (migration 084).
-- ---------------------------------------------------------------------------

DROP TRIGGER IF EXISTS trg_issue_project_dirty_dashboard ON issue;
DROP TRIGGER IF EXISTS trg_tu_dirty_dashboard           ON task_usage;
DROP TRIGGER IF EXISTS trg_issue_delete_dirty_dashboard ON issue;
DROP TRIGGER IF EXISTS trg_atq_dirty_dashboard          ON agent_task_queue;

DROP FUNCTION IF EXISTS task_usage_dashboard_rollup_lag_seconds();
DROP FUNCTION IF EXISTS rollup_task_usage_dashboard_daily();
DROP FUNCTION IF EXISTS rollup_task_usage_dashboard_daily_window(TIMESTAMPTZ, TIMESTAMPTZ);
DROP FUNCTION IF EXISTS enqueue_task_usage_dashboard_dirty_for_issue_project();
DROP FUNCTION IF EXISTS enqueue_task_usage_dashboard_dirty_for_issue_delete();
DROP FUNCTION IF EXISTS enqueue_task_usage_dashboard_dirty_for_tu();
DROP FUNCTION IF EXISTS enqueue_task_usage_dashboard_dirty_for_atq();

DROP TABLE IF EXISTS task_usage_dashboard_dirty;
DROP TABLE IF EXISTS task_usage_dashboard_rollup_state;
DROP TABLE IF EXISTS task_usage_dashboard_daily;

-- ---------------------------------------------------------------------------
-- task_usage_daily pipeline (migrations 073 / 077 / 082).
-- ---------------------------------------------------------------------------

DROP TRIGGER IF EXISTS trg_tu_dirty_rollup  ON task_usage;
DROP TRIGGER IF EXISTS trg_atq_dirty_rollup ON agent_task_queue;

DROP FUNCTION IF EXISTS task_usage_rollup_lag_seconds();
DROP FUNCTION IF EXISTS rollup_task_usage_daily();
DROP FUNCTION IF EXISTS rollup_task_usage_daily_window(TIMESTAMPTZ, TIMESTAMPTZ);
DROP FUNCTION IF EXISTS enqueue_task_usage_daily_dirty_for_tu();
DROP FUNCTION IF EXISTS enqueue_task_usage_daily_dirty_for_atq();

DROP TABLE IF EXISTS task_usage_daily_dirty;
DROP TABLE IF EXISTS task_usage_rollup_state;
DROP TABLE IF EXISTS task_usage_daily;
