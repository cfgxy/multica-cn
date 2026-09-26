-- Migration 344 drops the whole V1 plugin schema, including
-- plugin_installation_config and plugin_remote_mcp_secret, and its down
-- direction deliberately does not restore it. Every database that has
-- migrated past 344 therefore keeps this migration's schema_migrations row
-- while the objects it touches are already gone, so this rollback must treat
-- each of them as conditionally present rather than assume it exists.

DROP TABLE IF EXISTS plugin_remote_mcp_oauth_state;

DO $$
BEGIN
    IF to_regclass('plugin_installation_config') IS NULL THEN
        RETURN;
    END IF;
    IF to_regclass('plugin_remote_mcp_secret') IS NOT NULL THEN
        DELETE FROM plugin_remote_mcp_secret
        WHERE id IN (SELECT secret_ref FROM plugin_installation_config WHERE auth_type = 'oauth');
    END IF;
    DELETE FROM plugin_installation_config WHERE auth_type = 'oauth';
END
$$;

ALTER TABLE IF EXISTS plugin_installation_config
    DROP CONSTRAINT IF EXISTS plugin_installation_config_discovered_digest_check,
    DROP COLUMN IF EXISTS discovered_schema_digest,
    DROP COLUMN IF EXISTS discovered_tools;

ALTER TABLE IF EXISTS plugin_installation_config
    DROP CONSTRAINT IF EXISTS plugin_installation_config_auth_type_check;

ALTER TABLE IF EXISTS plugin_installation_config
    ADD CONSTRAINT plugin_installation_config_auth_type_check
    CHECK (auth_type IN ('none', 'bearer', 'header'));
