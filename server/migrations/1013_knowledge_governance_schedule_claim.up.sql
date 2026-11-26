-- Per-directory durable claims make a scheduled configuration window idempotent
-- even when another directory is active or fails in the same workspace pass.
CREATE TABLE knowledge_governance_schedule_claim (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    dir_id UUID NOT NULL,
    config_version INTEGER NOT NULL,
    schedule_bucket TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE knowledge_governance_schedule_claim IS
    'Committed schedule-window claims for one knowledge directory and governance config version.';
