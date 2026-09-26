-- Workspace teardown and cross-scope audit reads. prompt_quality_daily rows
-- are keyed by scope, so deleting a workspace has no other way to find them
-- than a full scan (see the workspace_delete manifest).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_prompt_quality_daily_workspace
    ON prompt_quality_daily (workspace_id);
