CREATE INDEX CONCURRENTLY audit_event_mcp_expiry_idx
    ON audit_event (expires_at)
    WHERE domain = 'mcp' AND expires_at IS NOT NULL;
