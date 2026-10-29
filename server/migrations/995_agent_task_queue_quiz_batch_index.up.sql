CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_queue_quiz_batch_agent_created_at
    ON agent_task_queue (agent_id, (context ->> 'quiz_batch_id'), created_at DESC)
    WHERE originator_source = 'quiz';
