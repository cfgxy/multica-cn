CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_task_state_audit_issue ON issue_task_state_audit (issue_id, created_at);
