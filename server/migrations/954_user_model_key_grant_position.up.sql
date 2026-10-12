-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 548_user_model_key_grant_position_index; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. Ordered grant listing per user.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_user_model_key_grant_user_position
    ON user_model_key_grant (user_id, position, upstream_id);
