-- RUYI-304: at most ONE 'pending' intent per (chat_session, context_revision).
-- This is the upsert conflict target: every message in a debounce window
-- extends the same row (later fire_at, latest sender); once the row leaves
-- 'pending' the next message on the same generation legitimately inserts a
-- fresh pending row for the next window.
CREATE UNIQUE INDEX CONCURRENTLY channel_chat_run_intent_pending_uidx
    ON channel_chat_run_intent (chat_session_id, context_revision)
    WHERE state = 'pending';
