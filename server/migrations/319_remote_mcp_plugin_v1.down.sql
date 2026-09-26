-- Migration 344 drops the whole V1 plugin schema and does not restore it on
-- the way down, so plugin_installation_config and plugin_contribution may be
-- absent here while this migration's schema_migrations row still exists.
-- DROP TRIGGER IF EXISTS and ALTER TABLE still fail on a missing table, so
-- guard on the relation itself.

DO $$
BEGIN
    IF to_regclass('plugin_installation_config') IS NOT NULL THEN
        DROP TRIGGER IF EXISTS trg_plugin_installation_config_append_only ON plugin_installation_config;
    END IF;
END
$$;

DROP TABLE IF EXISTS plugin_installation_config;
DROP TABLE IF EXISTS plugin_remote_mcp_secret;

ALTER TABLE IF EXISTS plugin_contribution
    DROP CONSTRAINT IF EXISTS plugin_contribution_type_check;

ALTER TABLE IF EXISTS plugin_contribution
    ADD CONSTRAINT plugin_contribution_type_check
    CHECK (type IN ('agent.skill.v1'));
