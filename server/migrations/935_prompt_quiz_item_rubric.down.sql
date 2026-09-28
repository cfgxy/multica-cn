ALTER TABLE prompt_quiz_item
    DROP COLUMN IF EXISTS rubric;

COMMENT ON COLUMN prompt_quiz_item.body IS NULL;
