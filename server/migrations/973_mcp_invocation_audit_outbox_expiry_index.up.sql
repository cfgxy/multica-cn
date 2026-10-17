CREATE INDEX CONCURRENTLY mcp_invocation_audit_outbox_expiry_idx
    ON mcp_invocation_audit_outbox (expires_at);
