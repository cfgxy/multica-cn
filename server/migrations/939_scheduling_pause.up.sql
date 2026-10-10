-- RUYI-608: the scheduling freeze switch.
--
-- One row per active freeze. agent_id IS NULL means a workspace-level
-- freeze (every agent in the workspace stops being claimable); a non-null
-- agent_id freezes exactly that agent. The claim gate itself lives in
-- ClaimAgentTask (a NOT EXISTS fence over this table), so a freeze survives
-- daemon and server restarts by construction: the state is server-side,
-- never daemon memory.
--
-- Semantics this table deliberately does NOT touch: queued tasks are kept
-- (not cancelled) and keep coalescing while frozen; running tasks finish
-- and report terminal state normally — complete/fail callbacks never read
-- this table. Resuming deletes the row; queued tasks then flow in the
-- existing created_at ASC claim order.
--
-- No foreign keys, per repo rule: workspace_id / agent_id / created_by
-- reference workspace.id / agent.id / member.user_id in the application
-- layer only. DeleteWorkspace sweeps the rows explicitly; agent deletion
-- only happens through workspace teardown (agents are archived otherwise),
-- so the workspace sweep is the single cleanup path.
--
-- The two partial unique indexes make a double freeze a no-op at the
-- storage layer: one workspace-level row per workspace, one agent-level
-- row per (workspace, agent). They are inlined in this batch rather than
-- built CONCURRENTLY — the table is brand new inside this one implicit
-- transaction, so no concurrent reader can observe an unconstrained build
-- window (same rationale as 925/929).

CREATE TABLE IF NOT EXISTS scheduling_pause (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    -- NULL = workspace-level freeze: every agent in the workspace is frozen.
    agent_id UUID,
    reason TEXT NOT NULL DEFAULT '',
    -- The human operator who froze scheduling (member.user_id). Owner/admin
    -- only at every write path, so this is always an administrator action.
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE scheduling_pause IS
    'Active scheduling freezes (RUYI-608). agent_id NULL = workspace-level freeze; non-null = agent-level. ClaimAgentTask fences on this table, so frozen agents cannot claim queued tasks; enqueue and coalesce continue; running tasks drain normally.';
COMMENT ON COLUMN scheduling_pause.agent_id IS
    'NULL freezes every agent in the workspace; a value freezes exactly that agent.';
COMMENT ON COLUMN scheduling_pause.reason IS
    'Free-text note from the operator who froze scheduling; audit trail, never interpreted.';
COMMENT ON COLUMN scheduling_pause.created_by IS
    'member.user_id of the workspace owner/admin who froze scheduling.';

CREATE UNIQUE INDEX IF NOT EXISTS idx_scheduling_pause_workspace_level
    ON scheduling_pause (workspace_id) WHERE agent_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_scheduling_pause_agent_level
    ON scheduling_pause (workspace_id, agent_id) WHERE agent_id IS NOT NULL;

-- Claim-fence probe shape: workspace_id = $agent_ws AND (agent_id IS NULL OR
-- agent_id = $agent). The two partial unique indexes above cover each arm
-- (BitmapOr), so no third index is added for it.
