CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_task_usage_model_call_totals_identity
    ON task_usage_model_call_totals (task_id, provider, model);
