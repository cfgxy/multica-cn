CREATE INDEX CONCURRENTLY idx_knowledge_lifecycle_event_entry
ON knowledge_lifecycle_event (entry_id, created_at DESC);
