CREATE UNIQUE INDEX CONCURRENTLY mcp_file_access_instance_binding_uidx
    ON mcp_file_access_instance_binding (instance_id, workspace_id);
