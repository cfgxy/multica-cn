-- name: CreateDaemonToken :one
-- owner_id binds the token to the runtime owner it was issued for; NULL is
-- reserved for managed daemon credentials whose ownership is checked by their
-- external connection record.
INSERT INTO daemon_token (token_hash, workspace_id, daemon_id, expires_at, owner_id)
VALUES (@token_hash, @workspace_id, @daemon_id, @expires_at, sqlc.narg('owner_id'))
RETURNING *;

-- name: GetDaemonTokenByHash :one
SELECT * FROM daemon_token
WHERE token_hash = $1 AND expires_at > now();

-- name: RenewDaemonToken :one
-- Renew the authenticated daemon credential in place. An online runtime owned
-- by the same owner is required so an old token cannot keep itself alive after
-- its runtime has gone offline, been removed, or been transferred.
UPDATE daemon_token AS dt
SET expires_at = now() + interval '24 hours'
WHERE dt.token_hash = @token_hash
  AND dt.workspace_id = @workspace_id
  AND dt.daemon_id = @daemon_id
  AND dt.owner_id = @owner_id
  AND dt.expires_at > now()
  AND EXISTS (
    SELECT 1
    FROM agent_runtime AS r
    WHERE r.workspace_id = dt.workspace_id
      AND r.daemon_id = dt.daemon_id
      AND r.owner_id = dt.owner_id
      AND r.status = 'online'
  )
RETURNING *;

-- name: DeleteDaemonTokensByWorkspaceAndDaemons :many
-- Deletes every daemon_token row matching the (workspace_id, daemon_id)
-- pairs implied by `daemon_ids`. Used by the member-revocation flow to
-- nuke tokens for all runtimes a leaving member owned in one shot.
-- Returns token_hash so the caller can invalidate auth.DaemonTokenCache
-- before the 10-minute TTL expires — without that invalidate, a daemon
-- can keep using its stale token until cache eviction even though the
-- DB row is gone.
DELETE FROM daemon_token
WHERE workspace_id = @workspace_id
  AND daemon_id = ANY(@daemon_ids::text[])
RETURNING token_hash;

-- name: DeleteExpiredDaemonTokens :exec
DELETE FROM daemon_token
WHERE expires_at <= now();

-- name: ListLegacyDaemonTokenOwners :many
-- Compatibility for Remote MCP daemon tokens minted before owner_id existed.
-- The daemon token and task_token were inserted in one transaction, so their
-- created_at values match. The caller accepts this result only when exactly one
-- live owner matches; LIMIT 2 is enough to detect ambiguity.
SELECT DISTINCT tt.user_id
FROM task_token tt
JOIN agent_task_queue q ON q.id = tt.task_id
JOIN agent_runtime r ON r.id = q.runtime_id
WHERE tt.workspace_id = @workspace_id
  AND tt.created_at = @created_at
  AND tt.expires_at > now()
  AND r.workspace_id = @workspace_id
  AND r.daemon_id = @daemon_id::text
LIMIT 2;
