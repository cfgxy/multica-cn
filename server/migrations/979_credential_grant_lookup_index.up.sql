CREATE INDEX CONCURRENTLY IF NOT EXISTS credential_grant_lookup_idx ON credential_grant (workspace_id, resource_scope, resource_id, subject_type, subject_id, status);
