CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_task_usage_model_call_evidence_identity
    ON task_usage_model_call_evidence (task_id, provider, evidence_id);
