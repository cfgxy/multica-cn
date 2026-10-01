CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_runtime_skill_discovery_identity
    ON runtime_skill_discovery (workspace_id, runtime_id, key);
