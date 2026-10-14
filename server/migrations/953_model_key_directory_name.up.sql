-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 547_model_key_directory_name_index; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. Directory listing lookup by key name.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_model_key_directory_entry_name
    ON model_key_directory_entry (source_id, api_key_name);
