-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 553_computer_runtime_model_config_identity; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. One model config per binding per runtime kind.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_computer_runtime_model_config_identity
    ON computer_runtime_model_config (binding_id, runtime_kind);
