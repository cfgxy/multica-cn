-- Down for 905_agent_session_context_gate, renumbered from the former migration 907 by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.

ALTER TABLE agent
    DROP COLUMN IF EXISTS session_max_context_tokens,
    DROP COLUMN IF EXISTS session_compact_pct;
