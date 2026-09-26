-- Pre-registered OAuth clients for the MCP authorization server (RUYI-209).
-- See migration 924 for the table and
-- docs/adr/001-mcp-oauth-behind-nextjs-proxy.md for the flow. No DCR: rows are
-- created by an operator, and created_by carries no DB foreign key.

-- name: CreateOAuthClient :one
INSERT INTO oauth_clients (client_id, client_secret_hash, name, redirect_uris, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetOAuthClientByClientID :one
SELECT * FROM oauth_clients
WHERE client_id = $1;

-- name: ListOAuthClients :many
SELECT * FROM oauth_clients
ORDER BY created_at ASC;

-- name: DeleteOAuthClient :exec
DELETE FROM oauth_clients
WHERE client_id = $1;
