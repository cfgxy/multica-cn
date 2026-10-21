CREATE INDEX CONCURRENTLY IF NOT EXISTS credential_vault_connector_revision_idx ON credential_vault (connector_id, connector_revision, status);
