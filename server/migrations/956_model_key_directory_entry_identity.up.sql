-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 550_model_key_directory_entry_identity; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. Directory entry identity per source.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_model_key_directory_entry_identity
    ON model_key_directory_entry (source_id, upstream_id);
