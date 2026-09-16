ALTER TABLE task_usage
    DROP COLUMN IF EXISTS max_context_tokens;
ALTER TABLE task_usage
    DROP COLUMN IF EXISTS compactions;
ALTER TABLE task_usage
    DROP COLUMN IF EXISTS turns;
