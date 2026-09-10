-- A series has at most one row per version number. This is the guard that makes
-- concurrent publishes safe: version assignment takes a series-level lock, and
-- if two publishes still raced past it, the second one fails here instead of
-- creating a duplicate v2.
--
-- Own file: CREATE UNIQUE INDEX CONCURRENTLY cannot run inside a transaction or
-- share a multi-command string.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_prompt_version_series_version
    ON marketplace_prompt_version (series_id, version)
    WHERE version IS NOT NULL;
