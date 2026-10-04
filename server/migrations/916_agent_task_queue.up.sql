-- Renumbered from the former migration 972 by the RUYI-359 Phase 2R
-- domain consolidation (gap-free 900+ ladder); content otherwise unchanged.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 972.up.sql (RUYI-359 consolidation)

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

-- >>> absorbed from 973.up.sql (RUYI-359 consolidation)

-- RUYI-292: who asked to stop this run, and when. Recorded at cancel acceptance
-- (status -> cancel_requested); consumed by the run detail surface (UI/MCP) to
-- show the canceller and to let clients compute the 30s unconfirmed-cancel
-- warning from cancel_requested_at. System-initiated batch cancels keep using
-- the existing error/failure_reason columns for the "why".
ALTER TABLE agent_task_queue ADD COLUMN cancel_requested_by_user_id UUID;
ALTER TABLE agent_task_queue ADD COLUMN cancel_requested_at TIMESTAMPTZ;
