-- RUYI-304: drop the run-trigger intent ledger. The table carries no external
-- side effects; dropping it returns the system to the pre-fix behavior
-- (in-memory debounce only, silent loss on crash).
DROP TABLE IF EXISTS channel_chat_run_intent;
