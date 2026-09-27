-- Batch progress: the periodic job asks "is this sweep's batch finished" and
-- the dashboard shows a batch's outcome breakdown. Both read one batch_id.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_prompt_quiz_result_batch
    ON prompt_quiz_result (batch_id);
