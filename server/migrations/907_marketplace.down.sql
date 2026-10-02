-- Down for 907_marketplace: reverse concatenation of the former migrations
-- 910, 914 (original order, descending), renumbered by the RUYI-359 Phase 2R
-- domain consolidation. Mapping rules:
-- server/cmd/migrate/9xx-consolidation.md.

-- The six indexes are dropped by their tables: they were built in the up
-- direction on relations this file removes, so there is nothing left to clean
-- up separately.
ALTER TABLE squad DROP COLUMN IF EXISTS marketplace_prompt_state;
ALTER TABLE agent DROP COLUMN IF EXISTS marketplace_prompt_state;
DROP TABLE IF EXISTS workspace_prompt_install;
DROP TABLE IF EXISTS marketplace_prompt_version;

-- >>> from former migration 910 (RUYI-359 Phase 2R)

-- >>> absorbed from 913.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_marketplace_listing_source_workspace;

-- >>> absorbed from 912.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_marketplace_listing_discovery;

-- >>> absorbed from 911.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_marketplace_listing_kind_name_key;

-- >>> lead 910.down.sql (drops the structures created above)

DROP TABLE IF EXISTS marketplace_listing;
