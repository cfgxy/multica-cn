-- Profile names are the only handle the Profile dropdown shows, so two
-- profiles sharing a name in one workspace would make the trigger label
-- ambiguous. Enforced in the database as well as the handler because the
-- create/rename paths are two separate round-trips and can race.
--
-- Own file: CREATE UNIQUE INDEX CONCURRENTLY cannot run inside a transaction
-- or share a multi-command string.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_execution_profile_workspace_name
    ON execution_profile (workspace_id, name);
