-- RUYI-359 consolidation: absorbs 930, 931, 932, 933, 934, 935, 936, 938, 956 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 956.down.sql (RUYI-359 consolidation)

-- Reverse of the grading columns this file added (originally its own
-- migration; its header mis-cited 950). A stored grade is recomputable only
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

-- >>> absorbed from 938.down.sql (RUYI-359 consolidation)

-- Irreversible cleanup: the rows the up migration deleted were orphans whose
-- workspace no longer exists, so there is nowhere to restore them from.
-- Down is intentionally a no-op.

-- >>> absorbed from 936.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_result_item;

-- >>> absorbed from 935.down.sql (RUYI-359 consolidation)

ALTER TABLE prompt_quiz_item
    DROP COLUMN IF EXISTS rubric;

COMMENT ON COLUMN prompt_quiz_item.body IS NULL;

-- >>> absorbed from 934.down.sql (RUYI-359 consolidation)

ALTER TABLE prompt_quiz_result
    DROP COLUMN IF EXISTS runtime_id,
    DROP COLUMN IF EXISTS run_model;

-- >>> absorbed from 933.down.sql (RUYI-359 consolidation)

-- Restore migration 929's outcome vocabulary.
ALTER TABLE prompt_quiz_result
    DROP CONSTRAINT IF EXISTS prompt_quiz_result_outcome_check;

UPDATE prompt_quiz_result SET outcome = 'passed' WHERE outcome = 'answered';

ALTER TABLE prompt_quiz_result
    ADD CONSTRAINT prompt_quiz_result_outcome_check
    CHECK (outcome IN ('passed', 'failed', 'errored'));

COMMENT ON COLUMN prompt_quiz_result.outcome IS NULL;

-- >>> absorbed from 932.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_item_workspace;

-- >>> absorbed from 931.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_result_batch;

-- >>> absorbed from 930.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quiz_result_baseline;

-- >>> lead 929.down.sql (drops the structures created above)

DROP TABLE IF EXISTS prompt_quiz_sweep_state;
DROP TABLE IF EXISTS prompt_quiz_result;
DROP TABLE IF EXISTS prompt_quiz_item;
