-- RUYI-626: tag each live_session row with the transport that opened it.
-- The direct-connect handoff creates rows through the same shared gate
-- chain as the gateway relay; without a mode column the two are
-- indistinguishable, so "direct sessions are auditable and separate from
-- gateway sessions" has no persistence to stand on.
--
-- Every row that predates this migration was relayed by the server gateway,
-- so the column backfills 'gateway' and stays 'gateway' for the gateway
-- write (openVoiceSession now passes it explicitly); the direct handoff
-- writes 'direct'. The CHECK pins the enum the way the status column does.
--
-- Same historical-log treatment as the rest of the table: additive, no FK,
-- reversible by dropping the column.
ALTER TABLE live_session
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'gateway'
        CHECK (mode IN ('gateway', 'direct'));
