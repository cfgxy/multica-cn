-- "What has this workspace published" is its own management view, and the
-- withdrawal / republication authority check reads by source workspace on every
-- publish, update and withdraw.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_listing_source_workspace
    ON marketplace_listing (source_workspace_id, kind, name_key);
