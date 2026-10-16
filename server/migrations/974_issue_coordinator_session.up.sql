CREATE TABLE issue_coordinator_session (
    issue_id UUID NOT NULL,
    leader_agent_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    session_id TEXT,
    generation BIGINT NOT NULL DEFAULT 1,
    snapshot_version BIGINT NOT NULL DEFAULT 0,
    snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_event_sequence BIGINT NOT NULL DEFAULT 0,
    active_task_id UUID,
    last_task_id UUID,
    continuity_state TEXT NOT NULL DEFAULT 'fresh',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
