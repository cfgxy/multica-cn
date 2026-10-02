-- RUYI-359 consolidation: absorbs 911, 912, 913 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 913.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_marketplace_listing_source_workspace;

-- >>> absorbed from 912.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_marketplace_listing_discovery;

-- >>> absorbed from 911.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_marketplace_listing_kind_name_key;

-- >>> lead 910.down.sql (drops the structures created above)

DROP TABLE IF EXISTS marketplace_listing;
