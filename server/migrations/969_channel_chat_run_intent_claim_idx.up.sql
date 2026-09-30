-- RUYI-304: serves the reconciler's due-row scan (state + compensation
-- deadline). Own single-statement migration for CONCURRENTLY (repo convention,
-- see migration 230).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_channel_chat_run_intent_claim
    ON channel_chat_run_intent (state, fire_at);
