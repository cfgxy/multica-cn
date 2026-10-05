-- RUYI-435 rollback: drop the workspace quick-reply catalog.
-- Plain data removal — the table holds no cross-references.

DROP INDEX IF EXISTS idx_quick_reply_workspace_position;
DROP INDEX IF EXISTS idx_quick_reply_workspace_name;
DROP TABLE IF EXISTS quick_reply;
