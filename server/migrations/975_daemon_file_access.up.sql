-- Typed read-only file targets and their independent daemonws operation ledger.
-- Relationships are enforced in application code; no foreign keys by policy.
CREATE TABLE daemon_file_access_target (
    id TEXT NOT NULL,
    workspace_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    roots JSONB NOT NULL DEFAULT '[]'::jsonb,
    workspace_acl JSONB NOT NULL DEFAULT '[]'::jsonb,
    member_acl JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE daemon_file_operation (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    target_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('file_list', 'file_metadata', 'file_read_chunk')),
    request JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed', 'succeeded', 'rejected', 'failed', 'timeout', 'expired')),
    lease_epoch BIGINT NOT NULL DEFAULT 0,
    result JSONB,
    error_class TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
