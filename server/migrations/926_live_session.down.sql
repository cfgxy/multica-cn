-- Down for 926_live_session (RUYI-425 stage 3).
--
-- The table is pure additive log storage: no other schema object references
-- it, so dropping it loses only the session history itself. Indexes drop
-- with the table.
DROP TABLE IF EXISTS live_session;
