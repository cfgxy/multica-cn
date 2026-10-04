CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_decisions_issue
    ON issue_decisions (issue_id, created_at);
