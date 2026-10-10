-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 541_computer_operation_id; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. id has a DEFAULT gen_random_uuid() but no PK in the domain stem; this makes it the addressable key for operation recovery.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS computer_operation_id
    ON computer_operation (id);
