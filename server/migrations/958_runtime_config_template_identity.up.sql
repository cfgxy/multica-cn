-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 552_runtime_config_template_identity; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. One template row per runtime kind.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_runtime_config_template_identity
    ON runtime_config_template (runtime_kind);
