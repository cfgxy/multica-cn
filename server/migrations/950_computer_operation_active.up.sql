-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 542_computer_operation_active; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. Partial unique index: at most one in-flight remote operation per (computer, account).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS computer_operation_active
    ON computer_operation (computer_id, username) WHERE state IN ('queued','running');
