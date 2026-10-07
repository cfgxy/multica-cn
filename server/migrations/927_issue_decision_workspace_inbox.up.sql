-- RUYI-494: workspace-level decision inbox. The aggregation lists an entire
-- workspace's cards filtered by status and ordered by created_at — a shape
-- the issue-scoped index (idx_issue_decisions_issue) cannot serve. One
-- statement per the concurrent-index rule: this file is exactly this build.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_decisions_workspace_status
    ON issue_decisions (workspace_id, status, created_at);
