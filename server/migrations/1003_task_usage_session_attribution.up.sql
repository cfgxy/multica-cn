-- RUYI-940: retain the provider session that produced each usage delta. A task
-- can contain both an old resumed-session delta and a fresh-session retry.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS session_id TEXT NOT NULL DEFAULT '';

-- Existing rows predate explicit attribution. Preserve their historical meaning
-- using the task session that was available before this column existed.
UPDATE task_usage tu
SET session_id = COALESCE(atq.session_id, '')
FROM agent_task_queue atq
WHERE atq.id = tu.task_id
  AND tu.session_id = '';

ALTER TABLE task_usage
    DROP CONSTRAINT IF EXISTS task_usage_task_id_provider_model_key;
