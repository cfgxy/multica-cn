ALTER TABLE audit_event
    ADD COLUMN invocation_id UUID,
    ADD COLUMN sensitive BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN expires_at TIMESTAMPTZ;

CREATE TABLE mcp_invocation_audit_outbox (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    actor_type TEXT NOT NULL,
    actor_id UUID,
    invocation_id UUID NOT NULL,
    issue_id UUID,
    task_id UUID,
    agent_id UUID,
    runtime_id UUID,
    reason TEXT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    sensitive BOOLEAN NOT NULL DEFAULT false,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    last_error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
