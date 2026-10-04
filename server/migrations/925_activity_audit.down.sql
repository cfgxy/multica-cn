-- Reverse of 925_activity_audit.up.sql (RUYI-355 Phase 1).
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS cancel_actor_id;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS cancel_actor_type;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS cancel_reason;

DROP TABLE IF EXISTS audit_event;
