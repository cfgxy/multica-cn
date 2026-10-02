-- RUYI-292: who asked to stop this run, and when. Recorded at cancel acceptance
-- (status -> cancel_requested); consumed by the run detail surface (UI/MCP) to
-- show the canceller and to let clients compute the 30s unconfirmed-cancel
-- warning from cancel_requested_at. System-initiated batch cancels keep using
-- the existing error/failure_reason columns for the "why".
ALTER TABLE agent_task_queue ADD COLUMN cancel_requested_by_user_id UUID;
ALTER TABLE agent_task_queue ADD COLUMN cancel_requested_at TIMESTAMPTZ;
