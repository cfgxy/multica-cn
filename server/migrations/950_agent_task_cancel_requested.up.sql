-- RUYI-292: persistent user-visible "cancel_requested" status.
--
-- Two-phase cancel for the user-initiated single-run path: the first phase
-- flips an in-flight row to cancel_requested (受理) and the daemon's cancel-ack
-- flips it to cancelled (生效) once the agent process tree is confirmed dead.
-- A persistent status (not a flag pair) is the only form that survives restarts
-- and reads identically across UI / MCP / CLI. Batch cancel paths keep writing
-- 'cancelled' directly; their semantics are out of scope here.
ALTER TABLE agent_task_queue
    DROP CONSTRAINT agent_task_queue_status_check,
    ADD CONSTRAINT agent_task_queue_status_check
        CHECK (status IN (
            'queued',
            'dispatched',
            'running',
            'completed',
            'failed',
            'cancelled',
            'waiting_local_directory',
            'deferred',
            'cancel_requested'
        ));
