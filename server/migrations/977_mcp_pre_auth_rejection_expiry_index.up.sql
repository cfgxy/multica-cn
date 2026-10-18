CREATE INDEX CONCURRENTLY mcp_pre_auth_rejection_expiry_idx
    ON mcp_pre_auth_rejection (expires_at);
