CREATE UNIQUE INDEX CONCURRENTLY uidx_knowledge_entry_identity
ON knowledge_entry (dir_id, key);
