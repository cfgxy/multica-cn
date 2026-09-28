-- Restore migration 929's outcome vocabulary.
ALTER TABLE prompt_quiz_result
    DROP CONSTRAINT IF EXISTS prompt_quiz_result_outcome_check;

UPDATE prompt_quiz_result SET outcome = 'passed' WHERE outcome = 'answered';

ALTER TABLE prompt_quiz_result
    ADD CONSTRAINT prompt_quiz_result_outcome_check
    CHECK (outcome IN ('passed', 'failed', 'errored'));

COMMENT ON COLUMN prompt_quiz_result.outcome IS NULL;
