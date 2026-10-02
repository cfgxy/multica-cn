-- Down for 914_retrospective, renumbered from the former migration 962 by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 964.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS uidx_retrospective_watermark_issue;

-- >>> absorbed from 963.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_retrospective_run_workspace;

-- >>> lead 962.down.sql (drops the structures created above)

DROP TABLE IF EXISTS retrospective_issue_watermark;
DROP TABLE IF EXISTS retrospective_run;
DROP TABLE IF EXISTS retrospective_config;
