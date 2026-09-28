-- Migration 344 drops plugin_identity and does not restore it on the way
-- down, so this rollback can run against a database where the table is
-- already gone while its schema_migrations row still exists.
ALTER TABLE IF EXISTS plugin_identity
    DROP CONSTRAINT IF EXISTS plugin_identity_scope_check,
    DROP COLUMN IF EXISTS owner_workspace_id;
