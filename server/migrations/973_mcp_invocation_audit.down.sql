DROP TABLE IF EXISTS mcp_invocation_audit_outbox;

ALTER TABLE audit_event
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS sensitive,
    DROP COLUMN IF EXISTS invocation_id;
