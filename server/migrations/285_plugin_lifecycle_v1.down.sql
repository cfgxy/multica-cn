-- Migration 344 drops every table created here and does not restore it on
-- the way down, so this rollback can run against a database where the tables
-- are already gone while its schema_migrations row still exists. DROP TRIGGER
-- IF EXISTS fails on a missing table, so guard on the relation itself.

DO $$
BEGIN
    IF to_regclass('plugin_binding') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_binding_append_only ON plugin_binding;
    END IF;
    IF to_regclass('plugin_grant') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_grant_append_only ON plugin_grant;
    END IF;
    IF to_regclass('plugin_contribution') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_contribution_append_only ON plugin_contribution;
    END IF;
    IF to_regclass('plugin_release') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_release_immutable ON plugin_release;
    END IF;
END
$$;

DROP FUNCTION IF EXISTS reject_plugin_append_only_update();
DROP FUNCTION IF EXISTS enforce_plugin_release_immutable();
DROP TABLE IF EXISTS plugin_binding;
DROP TABLE IF EXISTS plugin_grant;
DROP TABLE IF EXISTS plugin_installation;
DROP TABLE IF EXISTS plugin_contribution;
DROP TABLE IF EXISTS plugin_release;
DROP TABLE IF EXISTS plugin_identity;
