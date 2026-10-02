-- RUYI-359 consolidation: absorbs 966, 967, 968, 969 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 969.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS idx_channel_chat_run_intent_claim;

-- >>> absorbed from 968.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS channel_chat_run_intent_pending_uidx;

-- >>> absorbed from 967.down.sql (RUYI-359 consolidation)

ALTER TABLE channel_chat_run_intent
    DROP CONSTRAINT IF EXISTS channel_chat_run_intent_pkey;

-- >>> absorbed from 966.down.sql (RUYI-359 consolidation)

DROP INDEX IF EXISTS channel_chat_run_intent_id_uidx;

-- >>> lead 965.down.sql (drops the structures created above)

-- RUYI-304: drop the run-trigger intent ledger. The table carries no external
-- side effects; dropping it returns the system to the pre-fix behavior
-- (in-memory debounce only, silent loss on crash).
DROP TABLE IF EXISTS channel_chat_run_intent;
