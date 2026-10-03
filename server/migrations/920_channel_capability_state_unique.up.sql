-- RUYI-400: at most one probe-state row per (installation, channel,
-- capability) — the upsert conflict target. Kept as its own single-statement
-- migration so CONCURRENTLY runs outside an implicit transaction block
-- (repo convention: every non-PK index builds concurrently, even on new
-- tables; the PK above rides inside the CREATE TABLE transaction like
-- migration 915's).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channel_capability_state_uidx
    ON channel_capability_state (installation_id, channel_type, capability);
