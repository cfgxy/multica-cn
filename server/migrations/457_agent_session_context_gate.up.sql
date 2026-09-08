-- Session context gate (RUYI-107): per-agent thresholds that decide when a
-- follow-up run stops resuming its (agent, issue) session and starts a fresh
-- one carrying a bounded brief instead.
--
-- Concrete NOT NULL defaults rather than NULL-means-default: the gate has to
-- answer "resume or not" on every claim, and a nullable column would stack a
-- second sentinel (NULL = default) on top of the one the product already needs
-- (0 = disabled). One sentinel is enough.
--
-- session_max_context_tokens = 0 disables the gate entirely, restoring the
-- pre-RUYI-107 behavior where only the poisoned-session filters in
-- GetLastTaskSession stop a resume.
ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS session_max_context_tokens BIGINT NOT NULL DEFAULT 400000,
    ADD COLUMN IF NOT EXISTS session_compact_pct INT NOT NULL DEFAULT 80;
