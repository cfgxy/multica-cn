-- Pre-registered OAuth clients for the MCP authorization server (RUYI-209).
-- See migration 909 for the table and
-- docs/adr/001-mcp-oauth-behind-nextjs-proxy.md for the flow. No DCR: rows are
-- created by an operator, and created_by carries no DB foreign key.
--
-- RUYI-420 adds the lifecycle verbs the System Settings management UI and the
-- oauth_client CLI share: update, soft-disable, and in-place secret rotation.
-- The UI and the CLI call exactly these statements so the two surfaces cannot
-- drift.

-- name: CreateOAuthClient :one
INSERT INTO oauth_client (client_id, client_secret_hash, name, redirect_uris, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetOAuthClientByClientID :one
SELECT * FROM oauth_client
WHERE client_id = $1;

-- name: GetOAuthClientByID :one
SELECT * FROM oauth_client
WHERE id = $1;

-- name: ListOAuthClients :many
SELECT * FROM oauth_client
ORDER BY created_at ASC;

-- name: UpdateOAuthClient :one
-- Name and redirect-URI edit from the management UI / CLI update verb.
-- client_id and the secret hash are deliberately not writable here: identity
-- rotation is the rotate verb's job, and renaming a client_id would strand
-- every configured consumer.
UPDATE oauth_client SET
    name = $2,
    redirect_uris = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetOAuthClientDisabled :one
-- Soft-disable (pass timestamps) or re-enable (pass NULLs). Both columns are
-- written together so a row always carries its current state plus the actor
-- who produced it, mirroring SetUserDisabled.
UPDATE oauth_client SET
    disabled_at = sqlc.narg('disabled_at'),
    disabled_by = sqlc.narg('disabled_by'),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RotateOAuthClientSecret :one
-- In-place rotation: the new hash replaces the old one immediately, so the
-- old secret stops authenticating at the token endpoint on the next call
-- (RUYI-420 design: no dual-hash grace period).
UPDATE oauth_client SET
    client_secret_hash = $2,
    secret_updated_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteOAuthClient :exec
DELETE FROM oauth_client
WHERE client_id = $1;

-- name: DeleteOAuthClientByID :exec
-- The management surface addresses rows by UUID; the CLI keeps operating on
-- the public client_id.
DELETE FROM oauth_client
WHERE id = $1;

-- name: CountOAuthClients :one
-- Instance totals for the MCP Server status page.
SELECT count(*) AS total,
       count(*) FILTER (WHERE disabled_at IS NULL) AS active
FROM oauth_client;
