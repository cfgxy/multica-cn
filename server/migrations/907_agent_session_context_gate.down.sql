ALTER TABLE agent
    DROP COLUMN IF EXISTS session_max_context_tokens,
    DROP COLUMN IF EXISTS session_compact_pct;
