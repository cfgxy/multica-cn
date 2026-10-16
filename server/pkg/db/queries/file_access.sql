-- Typed file operation registry and heartbeat claim storage.

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
SELECT * FROM daemon_file_access_target WHERE id = $1 OR runtime_id::text = $1;

-- name: InsertFileOperation :one
INSERT INTO daemon_file_operation (id, workspace_id, runtime_id, target_id, kind, request)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: ClaimFileOperations :many
UPDATE daemon_file_operation SET status = 'claimed', lease_epoch = lease_epoch + 1, claimed_at = now()
WHERE id IN (
    SELECT candidate.id FROM daemon_file_operation AS candidate
    JOIN daemon_file_access_target AS target ON target.id = candidate.target_id
        AND target.runtime_id = candidate.runtime_id AND target.workspace_id = candidate.workspace_id
    WHERE candidate.runtime_id = $1 AND candidate.status = 'pending'
      AND target.enabled = TRUE AND target.roots <> '[]'::jsonb
    ORDER BY candidate.created_at, candidate.id LIMIT $2 FOR UPDATE SKIP LOCKED
) RETURNING id, target_id, kind, request, lease_epoch;

-- name: ExpireFileOperations :exec
UPDATE daemon_file_operation SET status = CASE WHEN status = 'pending' THEN 'expired' ELSE 'timeout' END,
    error_class = CASE WHEN status = 'pending' THEN 'target_unreachable' ELSE 'timeout' END, finished_at = now()
WHERE (status = 'pending' AND created_at < now() - interval '2 minutes')
   OR (status = 'claimed' AND claimed_at < now() - interval '5 minutes');

-- name: FinalizeFileOperation :one
UPDATE daemon_file_operation SET status = $3, result = $4, error_class = $5, finished_at = now()
WHERE id = $1 AND runtime_id = $2 AND status = 'claimed' AND lease_epoch = $6
RETURNING *;

-- name: ListFileOperations :many
SELECT * FROM daemon_file_operation WHERE workspace_id = $1 AND target_id = $2
ORDER BY created_at DESC, id DESC LIMIT $3;

-- name: GetFileOperation :one
SELECT * FROM daemon_file_operation WHERE id = $1;

-- name: CountInFlightFileOperations :one
SELECT count(*) FROM daemon_file_operation WHERE runtime_id = $1 AND status IN ('pending', 'claimed');
