-- RUYI-359 Phase 2R domain consolidation: merges the former migrations
-- 908, 915, 924 into one atomic migration (renumbered to 906_task_usage) on the gap-free
-- 900+ ladder. Statement bodies are unchanged except CREATE/DROP INDEX lost
-- the CONCURRENTLY keyword: every target is created earlier in this same
-- file or by an earlier migration, and the whole file runs as one implicit
-- transaction (914 precedent); existing environments converge via the
-- ledger rewrite and never re-run these files. Original-stem -> new-stem
-- mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.

-- Context-size snapshot (RUYI-107).
--
-- The existing token columns are BILLING counters: they accumulate every model
-- request a run made, so a long run that resent its history 40 times counts
-- that history 40 times. The session gate needs the opposite statistic — how
-- large the conversation currently IS, which is roughly the input side of the
-- LAST request the run made. Storing it separately keeps the billing columns
-- untouched and lets a provider that cannot report it stay NULL, which the
-- gate reads as "unknown" and resumes.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS context_tokens BIGINT;

-- >>> from former migration 915 (RUYI-359 Phase 2R)

-- Run-scoped observability (RUYI-154): turns / compactions / peak context
-- size, alongside the end-of-run context_tokens (RUYI-107). A long run that
-- goes bad gives no signal today whether it was starved by context pressure
-- (many compactions, context riding near the ceiling) or failed for an
-- unrelated reason — these three columns make that distinguishable per run.
--
-- All three are NULL-able with the same convention as context_tokens
-- (migration 908): NULL means "this daemon/backend could not measure it",
-- never a real zero. turns=0 for a run that made zero requests is
-- indistinguishable from "unknown" in practice, so the write path also
-- treats non-positive reports as NULL — see authoritativeTurns /
-- authoritativeCompactions / authoritativeMaxContextTokens in
-- internal/handler/daemon.go.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS turns INTEGER;
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS compactions INTEGER;
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS max_context_tokens BIGINT;

-- >>> from former migration 924 (RUYI-359 Phase 2R)

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
