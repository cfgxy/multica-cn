CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_ac_issue ON issue_acceptance_criteria (issue_id, position, created_at);
