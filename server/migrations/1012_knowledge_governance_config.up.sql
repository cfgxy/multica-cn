-- Independent, workspace-owned controls for the advisory knowledge-governance
-- scheduler. Existing retrospective selections are copied once as the legacy
-- equivalent; later changes never read retrospective_config.
CREATE TABLE knowledge_governance_config (
    workspace_id UUID PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    agent_id UUID,
    cadence TEXT NOT NULL DEFAULT 'daily' CHECK (cadence IN ('daily', 'weekly', 'monthly')),
    timezone TEXT NOT NULL DEFAULT 'UTC',
    last_scheduled_bucket TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    updated_by UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE knowledge_governance_config_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    config_version INTEGER NOT NULL,
    actor_id UUID,
    before_value JSONB NOT NULL DEFAULT '{}'::jsonb,
    after_value JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Preserve the old effective executor and enabled state exactly once. Rows
-- without a valid executor become disabled rather than unexpectedly enabling
-- governance during the cutover.
INSERT INTO knowledge_governance_config (workspace_id, enabled, agent_id, updated_by)
SELECT r.workspace_id,
       r.enabled AND a.id IS NOT NULL,
       CASE WHEN a.id IS NOT NULL THEN r.agent_id ELSE NULL::uuid END,
       r.updated_by
FROM retrospective_config r
LEFT JOIN agent a ON a.id = r.agent_id
  AND a.workspace_id = r.workspace_id
  AND a.archived_at IS NULL
  AND a.runtime_id IS NOT NULL
ON CONFLICT (workspace_id) DO NOTHING;

INSERT INTO knowledge_governance_config_audit (workspace_id, config_version, actor_id, before_value, after_value)
SELECT workspace_id,
       version,
       updated_by,
       '{}'::jsonb,
       jsonb_build_object(
           'enabled', enabled,
           'agent_id', agent_id,
           'cadence', cadence,
           'timezone', timezone,
           'source', 'retrospective_config_migration'
       )
FROM knowledge_governance_config;

COMMENT ON TABLE knowledge_governance_config IS
    'Workspace-owned execution controls for advisory knowledge governance; independent from retrospective_config.';
COMMENT ON TABLE knowledge_governance_config_audit IS
    'Immutable before/after audit rows for knowledge governance scheduler configuration.';
