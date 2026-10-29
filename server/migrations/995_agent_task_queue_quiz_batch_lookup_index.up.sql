CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_queue_quiz_batch_lookup
    ON agent_task_queue ((context ->> 'quiz_batch_id'), created_at DESC)
    WHERE originator_source = 'quiz';
