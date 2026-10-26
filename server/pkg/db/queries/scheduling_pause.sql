-- RUYI-608: queries for the scheduling freeze switch. The table and its
-- semantics are documented in migration 936; the claim gate itself is the
-- NOT EXISTS fence inside ClaimAgentTask (agent.sql) — these queries are the
-- management surface (freeze / unfreeze / inspect) plus the counts the API
-- surfaces as "frozen queue depth".

-- name: InsertAgentSchedulingPause :execrows
-- Freeze one agent. The ON CONFLICT DO NOTHING arm makes the insert
-- source-guarded: when ANY row already occupies the (workspace, agent)
-- agent-level slot — a first human write, the automatic loop's system
-- source row (created_by NULL, RUYI-859) or anything else — the statement
-- writes nothing and reports 0 affected rows, so the caller observes the
-- slot instead of taking it. Rows>0 is the paused transition (audit row +
-- realtime broadcast owed); rows=0 is a read-only no-op. The caller
-- reads the slot back with GetAgentSchedulingPause to report the CURRENT
-- row (first write's attribution preserved), keeping a double freeze
-- idempotent: the second call never rewrites the audit trail.
INSERT INTO scheduling_pause (workspace_id, agent_id, reason, created_by)
VALUES (@workspace_id, @agent_id, @reason, @created_by)
ON CONFLICT (workspace_id, agent_id) WHERE agent_id IS NOT NULL
DO NOTHING;

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

-- name: DeleteHumanAgentSchedulingPause :execrows
-- The human resume's precise-source delete: created_by NOT NULL selects the
-- human freezes only. The loop's system source row — created_by NULL with
-- the fixed 'error_rate_suppression' reason (RUYI-859 stage B) — is
-- invisible to this statement, so a human resume can neither lift an
-- automatic suppression nor fake a human-resumed transition on top of it;
-- the loop is that row's only releaser. Rows>0 is the resumed transition
-- (audit row + broadcast owed); rows=0 means the slot was empty or is held
-- by a row no human may release: read-only.
DELETE FROM scheduling_pause
WHERE workspace_id = @workspace_id
  AND agent_id = @agent_id
  AND created_by IS NOT NULL;

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

-- name: ListWorkspaceSchedulingPauses :many
-- Workspace-level freeze rows for a set of workspaces — the workspaces-list
-- DTO enrichment reads this once instead of GetWorkspaceSchedulingPause in
-- a loop (no N+1).
SELECT * FROM scheduling_pause
WHERE agent_id IS NULL AND workspace_id = ANY(@workspace_ids::uuid[]);

-- name: CountQueuedTasksPerWorkspace :many
-- Per-workspace frozen-backlog depth in one query — the workspaces-list DTO
-- enrichment reads this instead of CountQueuedTasksByWorkspace in a loop.
-- The scheduling_pause join scopes the count to frozen workspaces, mirroring
-- the detail endpoint (no freeze row → zero, not a live queue depth).
SELECT a.workspace_id, count(*) AS queued_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
JOIN scheduling_pause sp ON sp.workspace_id = a.workspace_id AND sp.agent_id IS NULL
WHERE a.workspace_id = ANY(@workspace_ids::uuid[]) AND atq.status = 'queued'
GROUP BY a.workspace_id;
