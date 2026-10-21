CREATE INDEX CONCURRENTLY IF NOT EXISTS credential_vault_workspace_created_idx ON credential_vault (workspace_id, created_at DESC);
