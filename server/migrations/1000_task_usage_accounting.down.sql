ALTER TABLE task_usage
    DROP COLUMN IF EXISTS baseline_cache_write_tokens,
    DROP COLUMN IF EXISTS baseline_cache_read_tokens,
    DROP COLUMN IF EXISTS baseline_output_tokens,
    DROP COLUMN IF EXISTS baseline_input_tokens,
    DROP COLUMN IF EXISTS raw_cache_write_tokens,
    DROP COLUMN IF EXISTS raw_cache_read_tokens,
    DROP COLUMN IF EXISTS raw_output_tokens,
    DROP COLUMN IF EXISTS raw_input_tokens,
    DROP COLUMN IF EXISTS usage_session_id,
    DROP COLUMN IF EXISTS accounting_mode;
