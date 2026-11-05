-- Bind daemon tokens to the runtime owner they were issued for.
--
-- No backfill: the current owner of a daemon_id may be a later re-registrant,
-- so it is not proof of who received an existing token. Legacy Remote MCP
-- tokens recover an owner only from the live task_token minted in the same
-- claim transaction; every other unbound non-managed token fails closed.
-- Plain column, no foreign key: a deleted owner simply stops matching.
ALTER TABLE daemon_token ADD COLUMN IF NOT EXISTS owner_id UUID;
