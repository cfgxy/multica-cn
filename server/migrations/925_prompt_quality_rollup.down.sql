-- RUYI-359 consolidation: absorbs 926, 927, 928 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 928.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_perplexity_score_workspace;

-- >>> absorbed from 927.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quality_daily_workspace;

-- >>> absorbed from 926.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_prompt_quality_daily_scope_day;

-- >>> lead 925.down.sql (drops the structures created above)

DROP TABLE IF EXISTS prompt_quality_rollup_state;
DROP TABLE IF EXISTS prompt_perplexity_score;
DROP TABLE IF EXISTS prompt_quality_daily;
