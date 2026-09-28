ALTER TABLE prompt_quiz_result
    DROP COLUMN IF EXISTS runtime_id,
    DROP COLUMN IF EXISTS run_model;
