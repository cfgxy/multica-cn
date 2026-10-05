-- OAuth grants (RUYI-420): one row per (client, user) authorization, the
-- revocation anchor the auth middleware consults after JWT signature
-- verification. No DB foreign keys per repo rule — client_id references
-- oauth_client.client_id and user_id references "user".id, resolved in
-- application code. Rows are never hard-deleted: revoke sets revoked_at, so
-- the "my authorizations" page and the audit trail keep their history.

-- name: UpsertOAuthGrant :one
-- Consent approval writes here. A live row with a different scope is updated
-- in place (re-consent narrowed or widened it); a revoked row is revived with
-- the fresh scope and timestamps. (client_id, user_id) is unique by
-- idx_oauth_grant_client_user.
INSERT INTO oauth_grant (client_id, user_id, scope)
VALUES ($1, $2, $3)
ON CONFLICT (client_id, user_id) DO UPDATE SET
    scope = EXCLUDED.scope,
    revoked_at = NULL,
    last_used_at = NULL
RETURNING *;

-- name: GetOAuthGrantByID :one
SELECT * FROM oauth_grant
WHERE id = $1;

-- name: GetOAuthGrantGateState :one
-- The auth-path read: grant liveness plus the owning client's liveness in
-- one query. client_missing distinguishes "client row deleted" (fail
-- closed) from "client enabled" (disabled_at NULL) so a deleted client's
-- tokens die even though the LEFT JOIN finds no row.
SELECT g.id, g.revoked_at, g.scope, c.disabled_at,
       (CASE WHEN c.client_id IS NULL THEN false ELSE true END) AS client_exists
FROM oauth_grant g
LEFT JOIN oauth_client c ON c.client_id = g.client_id
WHERE g.id = $1;

-- name: GetActiveOAuthGrantByClientUser :one
SELECT * FROM oauth_grant
WHERE client_id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: RevokeOAuthGrantByID :one
-- Idempotent in effect: an already-revoked row keeps its original
-- revoked_at (the WHERE skips the write, RETURNING still yields the row).
UPDATE oauth_grant SET
    revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeOAuthGrantsByClient :many
-- Disable/delete-side effect: every live grant of the client dies with it.
-- Returns the grant ids so the caller can drop the gate-cache entries
-- immediately instead of waiting out the cache TTL.
UPDATE oauth_grant SET
    revoked_at = now()
WHERE client_id = $1 AND revoked_at IS NULL
RETURNING id;

-- name: ListOAuthGrants :many
-- Instance-wide grant directory for the admin grants page: who authorized
-- which client, for what scope, when, and whether it still lives.
SELECT g.*, u.name AS user_name, u.email AS user_email, c.name AS client_name
FROM oauth_grant g
LEFT JOIN "user" u ON u.id = g.user_id
LEFT JOIN oauth_client c ON c.client_id = g.client_id
ORDER BY g.created_at DESC, g.id DESC;

-- name: ListOAuthGrantsByClient :many
-- The admin client detail view: every grant of one client with the
-- granting user's identity, newest first.
SELECT g.*, u.name AS user_name, u.email AS user_email
FROM oauth_grant g
LEFT JOIN "user" u ON u.id = g.user_id
WHERE g.client_id = $1
ORDER BY g.created_at DESC, g.id DESC;

-- name: ListOAuthGrantsByUser :many
-- The "my authorizations" page: every grant the signed-in user has ever
-- issued, live or revoked, newest first.
SELECT g.*, c.name AS client_name
FROM oauth_grant g
LEFT JOIN oauth_client c ON c.client_id = g.client_id
WHERE g.user_id = $1
ORDER BY g.created_at DESC, g.id DESC;

-- name: TouchOAuthGrantLastUsed :exec
-- Called from the auth gate on cache misses, mirroring the PAT last_used_at
-- throttle: at most one write per cache TTL window per grant.
UPDATE oauth_grant SET
    last_used_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: CountOAuthGrantsByClient :many
-- Per-client grant tallies for the admin client list: how many users ever
-- authorized, and how many of those authorizations still live.
SELECT client_id,
       count(*) AS grant_count,
       count(*) FILTER (WHERE revoked_at IS NULL) AS active_grant_count,
       max(last_used_at)::timestamptz AS last_used_at
FROM oauth_grant
GROUP BY client_id;

-- name: CountOAuthGrants :one
-- Instance totals for the MCP Server status page.
SELECT count(*) AS total,
       count(*) FILTER (WHERE revoked_at IS NULL) AS active
FROM oauth_grant;
