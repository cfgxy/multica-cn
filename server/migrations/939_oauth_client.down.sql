-- RUYI-359 consolidation: absorbs 940 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 940.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_oauth_client_client_id;

-- >>> lead 939.down.sql (drops the structures created above)

DROP TABLE IF EXISTS oauth_client;
