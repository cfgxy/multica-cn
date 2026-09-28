-- Bank maintenance and sweep enumeration both list one workspace's items. The
-- unique constraint from 929 leads with workspace_id but carries slug, so it
-- serves an exact-slug lookup and not an ordered listing.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_prompt_quiz_item_workspace
    ON prompt_quiz_item (workspace_id, active, created_at DESC);
