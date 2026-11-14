-- RUYI-941: durable Quiz scheduling controls.
--
-- Quiz is a formal self-evolution measurement surface (RUYI-185/RUYI-286), so
-- its bank and measurements remain intact when scheduling is disabled. A row is
-- an explicit workspace or agent override; no row means enabled, preserving the
-- existing default. The claim fence and sweep both resolve the most-specific
-- matching row, so this survives server/daemon restarts and races.
--
-- Queue rows are not deleted. Disabling cancels only queued/dispatched Quiz
-- tasks; running tasks drain normally and their terminal measurements remain
-- collectible. The control row plus the audit event are the operator trail.
CREATE TABLE IF NOT EXISTS prompt_quiz_schedule_control (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    -- NULL = workspace override; non-null = one-agent override.
    agent_id UUID,
    enabled BOOLEAN NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    updated_by UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE prompt_quiz_schedule_control IS
    'RUYI-941 durable Quiz scheduler overrides. Missing row defaults to enabled; agent rows override workspace rows. Quiz data is retained when scheduling is disabled.';
COMMENT ON COLUMN prompt_quiz_schedule_control.agent_id IS
    'NULL applies to every agent in the workspace; a value applies only to that agent.';
COMMENT ON COLUMN prompt_quiz_schedule_control.updated_by IS
    'member.user_id of the owner/admin who changed the Quiz scheduler override.';

CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_quiz_schedule_control_workspace
    ON prompt_quiz_schedule_control (workspace_id) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_quiz_schedule_control_agent
    ON prompt_quiz_schedule_control (workspace_id, agent_id) WHERE agent_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_prompt_quiz_schedule_control_agent_lookup
    ON prompt_quiz_schedule_control (workspace_id, agent_id, enabled);
