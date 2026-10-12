-- Down for 937_live_session_mode (RUYI-626). The column is pure additive
-- transport metadata on a log table: dropping it loses only the gateway /
-- direct tagging, no other schema object references it.
ALTER TABLE live_session DROP COLUMN mode;
