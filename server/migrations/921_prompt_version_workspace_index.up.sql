-- Workspace-level listing/audit views filter by workspace before scope.
CREATE INDEX CONCURRENTLY idx_prompt_version_workspace
    ON prompt_version (workspace_id, created_at DESC);
