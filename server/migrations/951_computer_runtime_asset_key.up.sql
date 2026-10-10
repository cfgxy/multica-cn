-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 543_computer_runtime_asset_key; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. One runtime asset probe row per binding per runtime.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS computer_runtime_asset_key
    ON computer_runtime_asset (binding_id, runtime_id);
