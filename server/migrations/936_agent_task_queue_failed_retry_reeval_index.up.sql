-- RUYI-579 W3: the retry re-evaluation sweep re-reads failed tasks whose
-- successor-driven retry deferral (fire_at) has expired. The row set is
-- near-empty in steady state — a failed row carries fire_at only between
-- "retry deferred behind a pending successor" and the next sweep tick — so
-- this partial index costs almost nothing to maintain while keeping the
-- per-tick lookup an index probe instead of a full scan.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_queue_failed_retry_reeval
    ON agent_task_queue (fire_at)
    WHERE status = 'failed' AND fire_at IS NOT NULL;
