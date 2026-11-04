ALTER TABLE agent_task_queue
DROP CONSTRAINT IF EXISTS agent_task_queue_no_action_output_check,
DROP CONSTRAINT IF EXISTS agent_task_queue_no_action_reason_check,
DROP COLUMN IF EXISTS comment_output_at,
DROP COLUMN IF EXISTS no_action_reason,
DROP COLUMN IF EXISTS no_action_at;
