-- RUYI-304: attach the primary key via the concurrently-built unique index
-- (same three-step pattern as migration 229).
ALTER TABLE channel_chat_run_intent
    ADD CONSTRAINT channel_chat_run_intent_pkey PRIMARY KEY USING INDEX channel_chat_run_intent_id_uidx;
