-- One open draft per series. Publishing a v2 starts from a fresh draft, and two
-- half-filled drafts for the same asset would make "continue the draft" in the
-- source object's status bar ambiguous.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_marketplace_prompt_version_series_draft
    ON marketplace_prompt_version (series_id)
    WHERE state = 'draft';
