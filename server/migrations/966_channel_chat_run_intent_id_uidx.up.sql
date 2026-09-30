-- RUYI-304: unique index backing channel_chat_run_intent's primary key
-- (attached in 967 USING INDEX). Kept in its own single-statement migration so
-- CONCURRENTLY runs outside an implicit transaction block (repo convention;
-- see migrations 228 and 208).
CREATE UNIQUE INDEX CONCURRENTLY channel_chat_run_intent_id_uidx
    ON channel_chat_run_intent (id);
