-- Preserve the distinction between a task delta and a provider session snapshot.
-- Existing rows remain legacy measurements and keep their historical totals.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS accounting_mode TEXT NOT NULL DEFAULT 'legacy',
    ADD COLUMN IF NOT EXISTS usage_session_id TEXT,
    ADD COLUMN IF NOT EXISTS raw_input_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS raw_output_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS raw_cache_read_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS raw_cache_write_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS baseline_input_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS baseline_output_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS baseline_cache_read_tokens BIGINT,
    ADD COLUMN IF NOT EXISTS baseline_cache_write_tokens BIGINT;

COMMENT ON COLUMN task_usage.accounting_mode IS
    'How the daemon accounted this row: legacy, delta, cumulative, or unknown.';
COMMENT ON COLUMN task_usage.usage_session_id IS
    'Provider session identity used to derive a cumulative snapshot baseline.';
COMMENT ON COLUMN task_usage.raw_input_tokens IS
    'Raw provider snapshot before task-delta accounting; NULL for legacy rows.';
COMMENT ON COLUMN task_usage.baseline_input_tokens IS
    'Provider snapshot at task start; NULL when a cumulative baseline was unavailable.';
