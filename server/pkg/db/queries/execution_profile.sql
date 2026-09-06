-- Execution profiles (RUYI-57). A workspace-level named set of per-agent
-- execution configuration; activating one overwrites runtime/model/thinking
-- level on exactly the agents the profile names. See migration 454 for the
-- tables. No DB foreign keys — workspace/agent/runtime integrity is enforced
-- by the handler, which re-validates every entry against the workspace on
-- activation.

-- name: CreateExecutionProfile :one
INSERT INTO execution_profile (workspace_id, name, description, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetExecutionProfileForWorkspace :one
SELECT * FROM execution_profile
WHERE id = $1 AND workspace_id = $2;

-- name: LockExecutionProfileForActivation :one
-- Serializes concurrent activations of the same profile. The activation path
-- reads the entry set, validates it and writes the agents in one transaction;
-- without this lock two simultaneous activations could interleave their agent
-- writes and leave the workspace on a mix of both profiles while the pointer
-- named only one.
SELECT * FROM execution_profile
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: ListExecutionProfiles :many
SELECT * FROM execution_profile
WHERE workspace_id = $1
ORDER BY created_at ASC;

-- name: CountExecutionProfileEntriesByProfile :many
-- Entry count per profile for the whole workspace, so the dropdown can render
-- "N members" for every row without one query per profile.
SELECT profile_id, count(*)::bigint AS entry_count
FROM execution_profile_entry
WHERE profile_id IN (SELECT id FROM execution_profile WHERE workspace_id = $1)
GROUP BY profile_id;

-- name: UpdateExecutionProfile :one
-- Partial update via COALESCE: NULL args leave the column unchanged.
UPDATE execution_profile
SET name        = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    updated_at  = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkExecutionProfileActivated :one
UPDATE execution_profile
SET last_activated_at = now(),
    updated_at        = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteExecutionProfile :exec
DELETE FROM execution_profile
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteExecutionProfileEntriesByProfile :exec
-- Application-layer cascade for DeleteExecutionProfile; runs in the same
-- transaction so a profile can never survive as a headless entry set.
DELETE FROM execution_profile_entry
WHERE profile_id = $1;

-- name: ListExecutionProfileEntries :many
SELECT * FROM execution_profile_entry
WHERE profile_id = $1
ORDER BY created_at ASC;

-- name: UpsertExecutionProfileEntry :one
-- One entry per (profile, agent): re-saving an agent's configuration replaces
-- it rather than accumulating rows. runtime_id and model are always written
-- together because an entry is only activatable when both are present.
INSERT INTO execution_profile_entry (profile_id, agent_id, runtime_id, model, thinking_level)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (profile_id, agent_id) DO UPDATE
SET runtime_id     = EXCLUDED.runtime_id,
    model          = EXCLUDED.model,
    thinking_level = EXCLUDED.thinking_level,
    updated_at     = now()
RETURNING *;

-- name: DeleteExecutionProfileEntry :exec
DELETE FROM execution_profile_entry
WHERE profile_id = $1 AND agent_id = $2;

-- name: SetWorkspaceActiveExecutionProfile :exec
UPDATE workspace SET active_execution_profile_id = $2, updated_at = now()
WHERE id = $1;

-- name: ClearWorkspaceActiveExecutionProfile :exec
-- Used when the active profile is deleted. Guarded on the profile id so a
-- delete of some OTHER profile never clears the pointer.
UPDATE workspace SET active_execution_profile_id = NULL, updated_at = now()
WHERE id = $1 AND active_execution_profile_id = $2;

-- name: ApplyExecutionProfileEntryToAgent :one
-- The activation write. Scoped by workspace_id so a stale or forged agent_id
-- from another workspace cannot be reached, and by archived_at so an archived
-- agent is reported as skipped instead of silently reconfigured.
--
-- thinking_level is tri-state, which one nullable parameter cannot express:
--   thinking_level_present = false → the entry has no opinion, keep the
--     agent's current value.
--   present = true, narg NULL      → the entry says "runtime default", so the
--     column is cleared. COALESCE could never reach this state, which is why
--     an entry saved with an empty thinking level used to leave a stale
--     `high` on the agent while runtime and model were overwritten.
--   present = true, narg set       → write that level.
UPDATE agent
SET runtime_id     = @runtime_id,
    runtime_mode   = @runtime_mode,
    model          = @model,
    thinking_level = CASE
        WHEN @thinking_level_present::boolean THEN sqlc.narg('thinking_level')
        ELSE thinking_level
    END,
    updated_at     = now()
WHERE id = @id AND workspace_id = @workspace_id AND archived_at IS NULL
RETURNING *;
