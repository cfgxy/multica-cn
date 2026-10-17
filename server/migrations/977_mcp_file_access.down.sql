DROP TABLE IF EXISTS mcp_pre_auth_rejection;
DROP TABLE IF EXISTS mcp_file_access_instance_binding;
ALTER TABLE daemon_file_operation DROP COLUMN IF EXISTS invocation_id;
