CREATE INDEX CONCURRENTLY audit_event_mcp_invocation_idx
    ON audit_event (invocation_id)
    WHERE domain = 'mcp' AND invocation_id IS NOT NULL;
