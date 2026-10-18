ALTER TABLE chat_session ADD COLUMN model_override TEXT;
ALTER TABLE agent_task_queue ADD COLUMN chat_model_snapshot TEXT;
