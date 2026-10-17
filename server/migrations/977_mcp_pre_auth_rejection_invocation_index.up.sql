CREATE UNIQUE INDEX CONCURRENTLY mcp_pre_auth_rejection_invocation_uidx
    ON mcp_pre_auth_rejection (invocation_id);
