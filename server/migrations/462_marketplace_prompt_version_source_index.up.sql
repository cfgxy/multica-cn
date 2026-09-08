-- The status bar on an agent/squad prompt tab asks "what did this object
-- publish, and is there an open draft" on every render, which is a lookup by
-- source object rather than by series.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_prompt_version_source
    ON marketplace_prompt_version (source_workspace_id, source_type, source_id);
