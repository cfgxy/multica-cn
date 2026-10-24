-- RUYI-874 link ①: drop the voice ↔ chat link column. The idempotent
-- chat_message write-back rows it produced stay — they are ordinary chat
-- history and outlive the link.

ALTER TABLE live_session DROP COLUMN IF EXISTS chat_session_id;
