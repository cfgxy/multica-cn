-- Discovery lists published public versions newest first. Partial on the two
-- state columns because everything else — drafts, withdrawn rows, private
-- drafts — is only ever read by id or by source object.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_prompt_version_discovery
    ON marketplace_prompt_version (published_at DESC, id)
    WHERE state = 'published' AND visibility = 'public';
