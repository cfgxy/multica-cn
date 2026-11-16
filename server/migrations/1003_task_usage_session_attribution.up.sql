-- RUYI-940: retain the provider session that produced each usage delta. A task
-- can contain both an old resumed-session delta and a fresh-session retry.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS session_id TEXT NOT NULL DEFAULT '';

-- Existing rows predate explicit attribution. There is no durable evidence that
-- a canonical row belongs to the task's current session, especially after a
-- resumed attempt and a fresh retry were merged into one row. Keep session_id
-- empty until a later write carries explicit attribution; migration 1005 adds
-- the boolean marker used to keep these rows out of cumulative baselines.

ALTER TABLE task_usage
    DROP CONSTRAINT IF EXISTS task_usage_task_id_provider_model_key;
