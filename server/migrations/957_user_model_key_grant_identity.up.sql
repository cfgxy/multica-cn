-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 551_user_model_key_grant_identity; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. A user holds at most one grant per source key.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_user_model_key_grant_identity
    ON user_model_key_grant (user_id, source_id, upstream_id);
