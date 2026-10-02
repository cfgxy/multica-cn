-- RUYI-359 consolidation: absorbs 946, 947, 948, 951, 952 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


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
