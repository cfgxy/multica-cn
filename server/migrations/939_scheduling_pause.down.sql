-- RUYI-608 rollback: drop the scheduling freeze table.
-- Plain data removal — a dropped freeze removes the gate with it; queued
-- tasks become claimable again, which is the same effect as resuming.

DROP INDEX IF EXISTS idx_scheduling_pause_agent_level;
DROP INDEX IF EXISTS idx_scheduling_pause_workspace_level;
DROP TABLE IF EXISTS scheduling_pause;
