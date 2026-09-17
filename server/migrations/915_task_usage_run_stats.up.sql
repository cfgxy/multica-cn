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
