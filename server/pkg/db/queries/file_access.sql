-- Typed file operation registry and heartbeat claim storage.

-- name: GetMCPFileReadPolicy :one
SELECT version, rules, updated_by, updated_at
FROM mcp_file_read_policy WHERE singleton = TRUE;

-- name: UpdateMCPFileReadPolicy :one
UPDATE mcp_file_read_policy
SET version = version + 1, rules = sqlc.arg('rules')::jsonb,
    updated_by = sqlc.arg('updated_by'), updated_at = now()
WHERE singleton = TRUE AND version = sqlc.arg('expected_version')
RETURNING version, rules, updated_by, updated_at;

-- name: UpsertFileAccessTarget :one
INSERT INTO daemon_file_access_target (id, workspace_id, runtime_id, enabled, roots, workspace_acl, member_acl, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id, runtime_id = EXCLUDED.runtime_id,
    enabled = EXCLUDED.enabled, roots = EXCLUDED.roots, workspace_acl = EXCLUDED.workspace_acl,
    member_acl = EXCLUDED.member_acl, updated_at = now()
WHERE daemon_file_access_target.workspace_id = EXCLUDED.workspace_id
  AND daemon_file_access_target.runtime_id = EXCLUDED.runtime_id
RETURNING *;

-- name: GetFileAccessTarget :one
SELECT * FROM daemon_file_access_target WHERE id = $1;

-- name: GetFileAccessTargetByRuntime :one
SELECT * FROM daemon_file_access_target WHERE id = $1 AND runtime_id = $2;

-- name: ListFileAccessTargetsByHost :many
SELECT * FROM daemon_file_access_target WHERE id = sqlc.arg('host') OR runtime_id::text = sqlc.arg('host');

-- name: InsertFileOperation :one
INSERT INTO daemon_file_operation (id, workspace_id, runtime_id, target_id, kind, request, requester_id, invocation_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, sqlc.narg('invocation_id')) RETURNING *;

-- name: UpsertMCPFileAccessInstanceBinding :one
INSERT INTO mcp_file_access_instance_binding (instance_id, workspace_id, target_id, enabled, updated_by, updated_at)
SELECT sqlc.arg('instance_id'), target.workspace_id, target.id, sqlc.arg('enabled'), sqlc.arg('updated_by'), now()
FROM daemon_file_access_target AS target
WHERE target.id = sqlc.arg('target_id') AND target.workspace_id = sqlc.arg('workspace_id')
ON CONFLICT (instance_id, workspace_id) DO UPDATE SET
    target_id = EXCLUDED.target_id, enabled = EXCLUDED.enabled,
    updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: GetMCPFileAccessInstanceBinding :one
SELECT * FROM mcp_file_access_instance_binding
WHERE instance_id = sqlc.arg('instance_id') AND workspace_id = sqlc.arg('workspace_id');

-- name: GetMCPFileAccessInstanceBindingTarget :one
SELECT target.* FROM mcp_file_access_instance_binding AS binding
JOIN daemon_file_access_target AS target ON target.id = binding.target_id
    AND target.workspace_id = binding.workspace_id
WHERE binding.instance_id = sqlc.arg('instance_id')
  AND binding.workspace_id = sqlc.arg('workspace_id')
  AND binding.enabled = TRUE;

-- name: GetFileOperationForInvocation :one
SELECT * FROM daemon_file_operation
WHERE id = sqlc.arg('id') AND invocation_id = sqlc.arg('invocation_id');

-- name: ClaimFileOperations :many
WITH candidates AS MATERIALIZED (
    SELECT candidate.id, target.roots
    FROM daemon_file_operation AS candidate
    JOIN daemon_file_access_target AS target ON target.id = candidate.target_id
        AND target.runtime_id = candidate.runtime_id AND target.workspace_id = candidate.workspace_id
    WHERE candidate.runtime_id = $1 AND candidate.status = 'pending'
      AND target.enabled = TRUE AND target.roots <> '[]'::jsonb
      AND candidate.requester_id IS NOT NULL
      AND target.workspace_acl ? candidate.workspace_id::text
      AND (target.member_acl = '[]'::jsonb OR target.member_acl ? candidate.requester_id::text)
    ORDER BY candidate.created_at, candidate.id LIMIT $2
    FOR UPDATE OF candidate SKIP LOCKED
), claimed AS (
    UPDATE daemon_file_operation AS operation
    SET status = 'claimed', lease_epoch = operation.lease_epoch + 1, claimed_at = now()
    FROM candidates
    WHERE operation.id = candidates.id
    RETURNING operation.id, operation.target_id, operation.kind, operation.request,
        operation.lease_epoch
)
SELECT claimed.id, claimed.target_id, claimed.kind, claimed.request, claimed.lease_epoch, candidates.roots
FROM claimed JOIN candidates ON candidates.id = claimed.id;

-- name: ExpireFileOperations :exec
UPDATE daemon_file_operation SET status = CASE WHEN status = 'pending' THEN 'expired' ELSE 'timeout' END,
    error_class = CASE WHEN status = 'pending' THEN 'target_unreachable' ELSE 'timeout' END, finished_at = now()
WHERE (status = 'pending' AND created_at < now() - interval '2 minutes')
   OR (status = 'claimed' AND claimed_at < now() - interval '5 minutes');

-- name: FinalizeFileOperation :one
UPDATE daemon_file_operation SET status = $3, result = $4, error_class = $5, finished_at = now()
WHERE id = $1 AND runtime_id = $2 AND status = 'claimed' AND lease_epoch = $6
RETURNING *;

-- name: FinalizeFileOperationForPolicy :one
UPDATE daemon_file_operation SET status = $3, result = $4, error_class = $5, finished_at = now()
WHERE id = $1 AND runtime_id = $2 AND status = 'claimed' AND lease_epoch = $6
  AND EXISTS (SELECT 1 FROM mcp_file_read_policy WHERE singleton = TRUE AND version = sqlc.arg('policy_version'))
RETURNING *;

-- name: ListFileOperations :many
SELECT * FROM daemon_file_operation WHERE workspace_id = $1 AND target_id = $2
ORDER BY created_at DESC, id DESC LIMIT $3;

-- name: GetFileOperation :one
SELECT * FROM daemon_file_operation WHERE id = $1;

-- name: CountInFlightFileOperations :one
SELECT count(*) FROM daemon_file_operation WHERE runtime_id = $1 AND status IN ('pending', 'claimed');
