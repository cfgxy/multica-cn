-- Down for 911_knowledge: reverse concatenation of the former migrations
-- 945, 950, 953, 954 (original order, descending), renumbered by the RUYI-359 Phase 2R
-- domain consolidation. Mapping rules:
-- server/cmd/migrate/9xx-consolidation.md.

DROP INDEX IF EXISTS idx_project_resource_local_dir_daemon;

-- >>> from former migration 953 (RUYI-359 Phase 2R)

DROP INDEX IF EXISTS idx_knowledge_dir_daemon;

-- >>> from former migration 950 (RUYI-359 Phase 2R)

ALTER TABLE knowledge_dir DROP COLUMN scan_requested;
ALTER TABLE knowledge_dir DROP COLUMN daemon_id;
ALTER TABLE proposal DROP COLUMN transfer_state;

-- >>> from former migration 945 (RUYI-359 Phase 2R)

-- >>> absorbed from 952.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS uidx_knowledge_dir_ultimate_active;

-- >>> absorbed from 951.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS uidx_knowledge_dir_ws_path;

-- >>> absorbed from 948.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_knowledge_scan_batch_dir;

-- >>> absorbed from 947.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS uidx_knowledge_entry_identity;

-- >>> absorbed from 946.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_knowledge_dir_workspace;

-- >>> lead 945.down.sql (drops the structures created above)

DROP TABLE IF EXISTS knowledge_scan_batch;
DROP TABLE IF EXISTS knowledge_entry;
DROP TABLE IF EXISTS knowledge_dir;
