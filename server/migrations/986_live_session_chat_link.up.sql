-- RUYI-874 link ①: voice session ↔ chat session link.
--
-- live_session carries a nullable chat_session_id so the voice gateway can
-- write the session's merged turns back into the SAME chat_session the voice
-- UI was opened from. Plain nullable UUID, no FK (repo rule: relationships
-- are resolved in application code — openVoiceSession validates the row
-- belongs to this workspace and this agent before storing it).

ALTER TABLE live_session ADD COLUMN IF NOT EXISTS chat_session_id UUID;
