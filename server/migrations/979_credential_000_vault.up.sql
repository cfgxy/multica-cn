CREATE TABLE credential_connector (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind = 'mcp_http_bearer'),
    target_url TEXT NOT NULL,
    adapter_policy TEXT NOT NULL CHECK (adapter_policy = 'mcp_http_v1'),
    auth_field TEXT NOT NULL CHECK (auth_field = 'authorization_bearer'),
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE credential_vault (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    target_service TEXT NOT NULL,
    connector_id UUID NOT NULL,
    connector_revision BIGINT NOT NULL CHECK (connector_revision > 0),
    purpose TEXT NOT NULL,
    credential_type TEXT NOT NULL CHECK (credential_type = 'machine'),
    status TEXT NOT NULL CHECK (status IN ('active', 'exhausted', 'revoked', 'expired')),
    encrypted_value JSONB NOT NULL,
    created_by UUID NOT NULL,
    replaced_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE credential_grant (
    id UUID NOT NULL,
    credential_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    resource_scope TEXT NOT NULL CHECK (resource_scope IN ('single_use', 'issue', 'project', 'workspace', 'global')),
    resource_id UUID,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('agent', 'squad')),
    subject_id UUID NOT NULL,
    effect TEXT NOT NULL CHECK (effect IN ('allow', 'deny')),
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked', 'exhausted', 'expired')),
    expires_at TIMESTAMPTZ,
    granted_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE credential_capability (
    id UUID NOT NULL,
    capability_hash BYTEA NOT NULL,
    daemon_id TEXT NOT NULL,
    task_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    connector_id UUID NOT NULL,
    connector_revision BIGINT NOT NULL CHECK (connector_revision > 0),
    session_public_key BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE credential_audit (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    credential_id UUID,
    grant_id UUID,
    event_type TEXT NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('allowed', 'denied', 'recorded')),
    reason_code TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'daemon', 'system')),
    actor_id TEXT NOT NULL,
    daemon_id TEXT,
    task_id UUID,
    agent_id UUID,
    issue_id UUID,
    purpose TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
