-- Down for 909_oauth_client, renumbered from the former migration 939 by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.

-- >>> absorbed from 940.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_oauth_client_client_id;

-- >>> lead 939.down.sql (drops the structures created above)

DROP TABLE IF EXISTS oauth_client;
