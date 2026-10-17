CREATE UNIQUE INDEX CONCURRENTLY mcp_file_access_operation_invocation_uidx
    ON daemon_file_operation (invocation_id) WHERE invocation_id IS NOT NULL;
