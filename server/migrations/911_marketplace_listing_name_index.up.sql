-- D3-A: `kind` + lowercase-normalised name is globally unique across every
-- workspace, and the uniqueness survives withdrawal — a tombstone keeps its
-- name reserved so a withdrawn listing cannot be impersonated by a different
-- publisher.
--
-- Own file: CREATE UNIQUE INDEX CONCURRENTLY cannot run inside a transaction or
-- share a multi-command string.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_listing_kind_name_key
    ON marketplace_listing (kind, name_key);
