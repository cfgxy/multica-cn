CREATE INDEX CONCURRENTLY idx_retrospective_run_workspace
ON retrospective_run (workspace_id, created_at DESC);
