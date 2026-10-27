-- Down for 915_channel_chat_run_intent, renumbered from the former migration 965 by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.

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
