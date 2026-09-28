-- The bank maintenance read shape (A4): every measurement of one workspace's
-- questions, grouped per question, newest first.
--
-- Not served by idx_prompt_quiz_result_baseline: that index leads with
-- (scope, scope_id, version), and the discrimination mark asks about a question
-- ACROSS versions and scopes — the whole point is whether the question has ever
-- separated anything. Without this index the bank panel scans every measurement
-- row on the deployment to filter one workspace's.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_prompt_quiz_result_item
    ON prompt_quiz_result (workspace_id, item_id, measured_at DESC);
