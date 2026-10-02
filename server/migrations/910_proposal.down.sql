-- Down for 910_proposal, renumbered from the former migration 943 by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 955.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS uidx_proposal_system_dir;

-- >>> absorbed from 944.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_proposal_workspace_status;

-- >>> lead 943.down.sql (drops the structures created above)

DROP TABLE IF EXISTS proposal;
