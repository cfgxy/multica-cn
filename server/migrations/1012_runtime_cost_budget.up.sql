-- RUYI-665: per-runtime model cost budgets (design decisions adopted from the
-- furtherref runtime-cost-budget evaluation, RUYI-653).
--
-- One row per scope: user_id IS NULL is the runtime total (blocks every user
-- when reached); a non-NULL user_id caps the tasks on this runtime whose agent
-- that user owns. A NULL limit means unlimited for that period, and a row
-- whose three limits are all NULL is deleted by the application rather than
-- kept, so "no row" is the only representation of "no budget". Limits are
-- ticks of 1e-10 USD (metrics.CostUSDTicksPerUSD), the same unit
-- task_usage.cost_usd_ticks uses, so a limit and the spend it gates are one
-- scale. The *_notified_period_start columns remember which period already got
-- its "limit reached" inbox notice so the notice fires once per period; a
-- changed limit clears them (see the upsert query).
--
-- No foreign keys by repository rule; the (runtime_id, user_id) scope relation
-- is enforced in application code. Runtime deletion (every teardown path),
-- member revocation and workspace deletion remove matching rows in
-- application code.
--
-- id is NOT declared inline as PRIMARY KEY: the repo convention (migrations
-- 327-329 / 332-334) builds the table without one, creates the backing unique
-- index CONCURRENTLY in its own single-statement migration (967), then attaches
-- it with PRIMARY KEY USING INDEX (968). An inline PRIMARY KEY would build its
-- index non-concurrently, which the project's migration rules forbid even on a
-- new table. The (runtime_id, user_id) scope index is 969 for the same reason.
CREATE TABLE runtime_cost_budget (
    id                              UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id                    UUID NOT NULL,
    runtime_id                      UUID NOT NULL,
    user_id                         UUID,
    daily_limit_usd_ticks           BIGINT,
    weekly_limit_usd_ticks          BIGINT,
    monthly_limit_usd_ticks         BIGINT,
    daily_notified_period_start     TIMESTAMPTZ,
    weekly_notified_period_start    TIMESTAMPTZ,
    monthly_notified_period_start   TIMESTAMPTZ,
    updated_by                      UUID,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT runtime_cost_budget_limits_positive CHECK (
        (daily_limit_usd_ticks   IS NULL OR daily_limit_usd_ticks   > 0) AND
        (weekly_limit_usd_ticks  IS NULL OR weekly_limit_usd_ticks  > 0) AND
        (monthly_limit_usd_ticks IS NULL OR monthly_limit_usd_ticks > 0)
    )
);

COMMENT ON TABLE runtime_cost_budget IS
    'Per-runtime model cost budgets: runtime total (user_id NULL) plus per-user scopes, each with optional daily/weekly/monthly UTC-window USD limits in 1e-10 USD ticks. No row = no limit.';
COMMENT ON COLUMN runtime_cost_budget.user_id IS
    'NULL = runtime-total scope (blocks every user); non-NULL = caps tasks whose agent this user owns.';
COMMENT ON COLUMN runtime_cost_budget.daily_notified_period_start IS
    'UTC start of the daily period that already received its limit-reached inbox notice; NULL = not yet notified this period.';
