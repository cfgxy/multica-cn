CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_clause_injection_observation_workspace_time
    ON clause_injection_observation (workspace_id, created_at);
