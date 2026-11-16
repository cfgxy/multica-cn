CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_task_usage_task_session_provider_model
    ON task_usage (task_id, session_id, provider, model);
