-- Down for 903_execution_profile, renumbered from the former migration 904 by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 906.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_execution_profile_entry_profile_agent;

-- >>> absorbed from 905.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_execution_profile_workspace_name;

-- >>> lead 904.down.sql (drops the structures created above)

ALTER TABLE workspace DROP COLUMN IF EXISTS active_execution_profile_id;
DROP TABLE IF EXISTS execution_profile_entry;
DROP TABLE IF EXISTS execution_profile;
