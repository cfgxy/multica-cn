-- Discovery lists published rows by kind then name, matching the static
-- catalog's own ordering so the merged listing is stable. Partial on state
-- because withdrawn tombstones are only ever read by (kind, name_key) or by
-- source workspace.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_listing_discovery
    ON marketplace_listing (kind, name_key)
    WHERE state = 'published';
