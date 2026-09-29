CREATE INDEX CONCURRENTLY idx_knowledge_scan_batch_dir
ON knowledge_scan_batch (dir_id, started_at DESC);
