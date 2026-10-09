-- RUYI-608: queries for the scheduling freeze switch. The table and its
-- semantics are documented in migration 936; the claim gate itself is the
-- NOT EXISTS fence inside ClaimAgentTask (agent.sql) — these queries are the
-- management surface (freeze / unfreeze / inspect) plus the counts the API
-- surfaces as "frozen queue depth".

-- name: UpsertAgentSchedulingPause :one
-- Freeze one agent. The ON CONFLICT arm is a no-op against the partial
-- unique index (idx_scheduling_pause_agent_level), so a double freeze is
-- idempotent: the second call returns the ORIGINAL row, preserving the first
-- operator's created_by / created_at / reason instead of letting a later
-- caller quietly rewrite the audit trail. Pass an empty reason to explicitly
-- clear it on a deliberate re-freeze.
INSERT INTO scheduling_pause (workspace_id, agent_id, reason, created_by)
VALUES (@workspace_id, @agent_id, @reason, @created_by)
ON CONFLICT (workspace_id, agent_id) WHERE agent_id IS NOT NULL
DO UPDATE SET workspace_id = EXCLUDED.workspace_id
RETURNING *;

-- name: UpsertWorkspaceSchedulingPause :one
-- Freeze every agent in the workspace (agent_id NULL). Same idempotency
-- contract as UpsertAgentSchedulingPause, against
-- idx_scheduling_pause_workspace_level.
INSERT INTO scheduling_pause (workspace_id, agent_id, reason, created_by)
VALUES (@workspace_id, NULL, @reason, @created_by)
ON CONFLICT (workspace_id) WHERE agent_id IS NULL
DO UPDATE SET workspace_id = EXCLUDED.workspace_id
RETURNING *;

-- name: GetAgentSchedulingPause :one
SELECT * FROM scheduling_pause
WHERE workspace_id = @workspace_id AND agent_id = @agent_id;

-- name: GetWorkspaceSchedulingPause :one
SELECT * FROM scheduling_pause
WHERE workspace_id = @workspace_id AND agent_id IS NULL;

-- name: GetSchedulingPauseForAgent :one
-- The service-side probe mirroring the ClaimAgentTask fence exactly:
-- the agent is frozen iff its workspace has a workspace-level row OR it has
-- its own agent-level row. Used for the cheap pre-claim check that makes the
-- claim log emit a recognizable `scheduling_paused` outcome; the SQL fence
-- remains the authority.
SELECT sp.* FROM scheduling_pause sp
WHERE sp.workspace_id = @workspace_id
  AND (sp.agent_id IS NULL OR sp.agent_id = @agent_id)
ORDER BY sp.agent_id NULLS LAST
LIMIT 1;

-- name: DeleteAgentSchedulingPause :exec
DELETE FROM scheduling_pause
WHERE workspace_id = @workspace_id AND agent_id = @agent_id;

-- name: DeleteWorkspaceSchedulingPause :exec
DELETE FROM scheduling_pause
WHERE workspace_id = @workspace_id AND agent_id IS NULL;

-- name: DeleteAllSchedulingPausesByWorkspace :exec
-- Workspace teardown sweep; wired into DeleteWorkspace's CTE chain.
DELETE FROM scheduling_pause
WHERE workspace_id = @workspace_id;

-- name: ListSchedulingPausesByWorkspace :many
SELECT * FROM scheduling_pause
WHERE workspace_id = @workspace_id
ORDER BY created_at ASC;

-- name: CountQueuedTasksForAgent :one
-- Frozen queue depth for one agent. 'queued' only: dispatched/running rows
-- are not frozen — they belong to the drain, not to the freeze.
SELECT count(*) FROM agent_task_queue
WHERE agent_id = @agent_id AND status = 'queued';

-- name: CountQueuedTasksByWorkspace :one
-- Frozen queue depth for a whole workspace (all its agents' queued rows).
SELECT count(*) FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = @workspace_id AND atq.status = 'queued';

-- name: CountQueuedTasksByWorkspacePerAgent :many
-- Per-agent frozen queue depth for the whole workspace in one query — the
-- agents-list DTO enrichment reads this instead of CountQueuedTasksForAgent
-- in a loop (no N+1).
SELECT atq.agent_id, count(*) AS queued_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = @workspace_id AND atq.status = 'queued'
GROUP BY atq.agent_id;

-- name: ListAgentRuntimesByWorkspace :many
-- Distinct runtime bindings of the workspace's bound agents. Resume uses it
-- to bump the EmptyClaim cache + daemon wakeup for every runtime that could
-- be holding a cached "no queued task" verdict, so consumption resumes on
-- the next poll instead of after EmptyClaimCacheTTL.
SELECT DISTINCT runtime_id FROM agent
WHERE workspace_id = @workspace_id AND runtime_id IS NOT NULL;
