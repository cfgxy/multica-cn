-- RUYI-670: ported from trustbe-cn/multica @ a21d984ba75ae050f404e14e4c6a8bffcca62a9c
-- (fork migration 537_computer_binding_unique; original author yunfeng, trustbe.cn). Renumbered into
-- this repo's 9xx range. Unique per Linux account across the whole instance: one (computer, username) binding can belong to only one person.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS computer_binding_account
    ON computer_binding (computer_id, username);
