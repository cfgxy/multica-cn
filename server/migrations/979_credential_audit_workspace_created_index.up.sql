CREATE INDEX CONCURRENTLY IF NOT EXISTS credential_audit_workspace_created_idx ON credential_audit (workspace_id, created_at DESC);
