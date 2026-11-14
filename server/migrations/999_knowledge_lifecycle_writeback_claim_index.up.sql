CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_knowledge_lifecycle_event_writeback_claim
ON knowledge_lifecycle_event (writeback_state, writeback_claimed_at)
WHERE writeback_state = 'in_progress';
