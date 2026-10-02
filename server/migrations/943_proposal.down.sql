-- RUYI-359 consolidation: absorbs 944, 955 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 955.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS uidx_proposal_system_dir;

-- >>> absorbed from 944.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_proposal_workspace_status;

-- >>> lead 943.down.sql (drops the structures created above)

DROP TABLE IF EXISTS proposal;
