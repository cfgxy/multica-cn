-- Per-tool-result error flag (RUYI-184, self-evolution phase 2): the D4
-- "turn failure rate" dimension is the share of a run's tool results that
-- came back as errors, and that signal was being discarded at write time.
--
-- Every runtime backend already parses it — claude.go reads is_error off the
-- tool_result block, codex reads a per-item status — but the daemon's
-- ReportTaskMessages path only ever shipped Seq/Type/Tool/Content/Input/
-- Output, so the column to receive it never existed.
--
-- NULL-able with the same convention as task_usage.context_tokens (migration
-- 908) and task_usage.turns (915): NULL means "this runtime, or this message
-- type, cannot report it" — never a real false. A tool_result from a backend
-- that does not surface an error flag, and every message type that is not a
-- tool result, both stay NULL. D4 therefore reports "no data" rather than 0%
-- for runs predating this migration, and the dashboard says so explicitly
-- (ADR-002 T5): this history cannot be backfilled, because the flag was
-- never persisted.
ALTER TABLE task_message
    ADD COLUMN IF NOT EXISTS is_error BOOLEAN;

COMMENT ON COLUMN task_message.is_error IS
    'Whether this tool result reported an error (RUYI-184, D4). NULL = the runtime or message type cannot report it, never a real false; history before this column is permanently NULL.';
