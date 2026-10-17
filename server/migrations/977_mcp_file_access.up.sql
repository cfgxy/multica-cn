ALTER TABLE daemon_file_operation ADD COLUMN invocation_id UUID;

CREATE TABLE mcp_file_access_instance_binding (
    instance_id TEXT NOT NULL,
    workspace_id UUID NOT NULL,
    target_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_by UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mcp_pre_auth_rejection (
    invocation_id UUID NOT NULL,
    instance_id TEXT NOT NULL,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('unknown_tool', 'missing_workspace', 'invalid_workspace', 'unauthenticated_actor', 'entry_rejected')),
    file_tool_name TEXT CHECK (file_tool_name IS NULL OR file_tool_name IN ('list_files', 'get_file_metadata', 'read_file_chunk')),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
