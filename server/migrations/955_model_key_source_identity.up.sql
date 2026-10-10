-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 549_model_key_source_identity; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. Singleton source row identity (id is constrained to 1 by CHECK).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_model_key_source_identity
    ON model_key_source (id);
