CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_clause_injection_observation_task_scope_time
    ON clause_injection_observation (task_id, carrier_scope, carrier_scope_id, created_at);
