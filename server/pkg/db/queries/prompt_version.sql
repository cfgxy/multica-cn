-- Prompt version history (RUYI-183, self-evolution phase 1).
--
-- One handler drives all four tiers (workspace/project/squad/agent) through
-- a scope-keyed dispatch table, so the lock/update queries below are named
-- per scope but otherwise identical in shape: lock the owning entity row,
-- then write its business column. Locking the entity row itself (rather
-- than prompt_version) is what makes NextPromptVersion race-free even
-- though a scope can start with zero prior versions — there is no row to
-- lock in prompt_version yet, but the workspace/project/squad/agent row
-- always exists.

-- name: LockWorkspaceForPromptVersion :one
SELECT id FROM workspace WHERE id = $1 FOR UPDATE;

-- name: LockProjectForPromptVersion :one
SELECT id FROM project WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: LockSquadForPromptVersion :one
SELECT id FROM squad WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: LockAgentForPromptVersion :one
SELECT id FROM agent WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: UpdateWorkspaceContextForPromptVersion :one
UPDATE workspace SET context = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateProjectInstructionsForPromptVersion :one
UPDATE project SET instructions = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateSquadInstructionsForPromptVersion :one
UPDATE squad SET instructions = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateAgentInstructionsForPromptVersion :one
UPDATE agent SET instructions = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: NextPromptVersion :one
-- Called while the owning entity row is locked by one of the Lock*ForPromptVersion
-- queries above, so this read cannot slide under a concurrent write; the unique
-- index on (scope, scope_id, version) is the second line of defense.
SELECT COALESCE(MAX(version), 0)::int + 1 AS next_version
FROM prompt_version
WHERE scope = $1 AND scope_id = $2;

-- name: CreatePromptVersion :one
INSERT INTO prompt_version (
    workspace_id, scope, scope_id, version, content, content_sha256,
    source, source_version, change_note, scanner_revision, gate_result,
    author_user_id, author_note_issue_id
) VALUES (
    @workspace_id, @scope, @scope_id, @version, @content, @content_sha256,
    @source, sqlc.narg('source_version'), @change_note, @scanner_revision, @gate_result,
    sqlc.narg('author_user_id'), sqlc.narg('author_note_issue_id')
)
RETURNING *;

-- name: ListPromptVersions :many
SELECT * FROM prompt_version
WHERE scope = $1 AND scope_id = $2
ORDER BY version DESC
LIMIT $3 OFFSET $4;

-- name: CountPromptVersions :one
SELECT count(*) FROM prompt_version WHERE scope = $1 AND scope_id = $2;

-- name: GetPromptVersionByScopeVersion :one
SELECT * FROM prompt_version
WHERE scope = $1 AND scope_id = $2 AND version = $3;

-- name: GetLatestPromptVersion :one
SELECT * FROM prompt_version
WHERE scope = $1 AND scope_id = $2
ORDER BY version DESC
LIMIT 1;

-- name: SetAgentTaskQueuePromptVersions :exec
-- Run-level attribution (RUYI-183 T2): records which prompt_version.version
-- of each injected tier a claimed run executed with. Written once at claim
-- time; absent keys mean "tier not injected", never version 0.
UPDATE agent_task_queue SET prompt_versions = $2
WHERE id = $1;
