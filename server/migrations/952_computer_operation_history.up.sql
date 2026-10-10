-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 544_computer_operation_history; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. Descending history listing per binding.
CREATE INDEX CONCURRENTLY IF NOT EXISTS computer_operation_history
    ON computer_operation (binding_id, created_at DESC);
