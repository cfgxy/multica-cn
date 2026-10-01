-- Reverse of 950: drop the grading columns. A stored grade is recomputable only
-- while the answer transcript still exists in task_message, so dropping these
-- is a lossy rollback for measurements whose run has since been garbage
-- collected — acceptable for a column-level rollback, and the reason the up
-- migration is additive (a downgrade never rewrites item rows).

ALTER TABLE prompt_quiz_result
    DROP COLUMN IF EXISTS graded_at,
    DROP COLUMN IF EXISTS score_detail,
    DROP COLUMN IF EXISTS score;

ALTER TABLE prompt_quiz_item
    DROP COLUMN IF EXISTS rubric_checks,
    DROP COLUMN IF EXISTS difficulty,
    DROP COLUMN IF EXISTS tags;
