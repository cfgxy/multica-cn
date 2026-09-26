-- The baseline comparison's only read shape: every measurement of one scope at
-- one version, for one item revision, ordered by when it was taken. The unique
-- constraint from 929 is on task_id and serves none of it.
--
-- item_id sits after version because the comparison first pins the two version
-- groups and then walks the items within them; measured_at last so a "latest N
-- repeats" window is a range scan rather than a sort.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_prompt_quiz_result_baseline
    ON prompt_quiz_result (scope, scope_id, version, item_id, measured_at DESC);
