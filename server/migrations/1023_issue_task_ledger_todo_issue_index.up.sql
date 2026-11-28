CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_todos_issue ON issue_todos (issue_id, position, created_at);
