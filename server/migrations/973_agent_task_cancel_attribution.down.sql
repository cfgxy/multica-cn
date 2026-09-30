ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS cancel_requested_by_user_id;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS cancel_requested_at;
