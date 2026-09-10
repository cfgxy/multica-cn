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
