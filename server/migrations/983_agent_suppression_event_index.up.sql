-- RUYI-859 stage A: the agent_suppression_event "recent events" read on the
-- agent detail page (occurred_at DESC newest-first, per agent).
--
-- Separate single-statement file per the repo index rule: a CONCURRENTLY
-- build cannot share the multi-statement 982 stem transaction. This file is
-- executed outside an explicit transaction by the migration runner.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_suppression_event_agent
    ON agent_suppression_event (agent_id, occurred_at DESC);
