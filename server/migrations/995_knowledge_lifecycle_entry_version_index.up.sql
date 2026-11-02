CREATE UNIQUE INDEX CONCURRENTLY idx_knowledge_entry_version_entry
ON knowledge_entry_version (entry_id, version DESC);
