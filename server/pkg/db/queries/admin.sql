-- Admin account-management queries (RUYI-47). Every statement here backs an
-- instance-level /api/admin endpoint guarded by RequireSuperAdmin; none of
-- them consult workspace membership.

-- name: GetUserAdminState :one
-- Lightweight auth-path read: only the columns the disabled check and the
-- super-admin guard need. The auth middleware runs this on user-state cache
-- misses, so it must stay a single narrow index scan on the primary key.
SELECT id, email, is_super_admin, disabled_at FROM "user"
WHERE id = $1;

-- name: SetUserSuperAdmin :one
UPDATE "user" SET
    is_super_admin = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CountActiveSuperAdminsExcluding :one
-- Number of enabled super admins other than $1. The revoke path requires
-- this to stay >= 1 so the instance can never lock itself out.
SELECT count(*) FROM "user"
WHERE is_super_admin = TRUE
  AND disabled_at IS NULL
  AND id <> $1;

-- name: SetUserDisabled :one
-- Disable (pass valid timestamps) or re-enable (pass NULLs). Both columns
-- are written together so a row always carries its current state plus the
-- actor who produced it; a NULL pair means "enabled".
UPDATE "user" SET
    disabled_at = sqlc.narg('disabled_at'),
    disabled_by = sqlc.narg('disabled_by'),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListAllUsers :many
-- Instance-wide user directory for the admin user-management page. Search
-- matches email or display name; an empty @query disables filtering.
-- workspace_count is a denormalized LEFT JOIN aggregate — the admin page
-- shows it per row and the count must include users with no memberships.
SELECT u.*,
       COALESCE(m.workspace_count, 0) AS workspace_count
FROM "user" u
LEFT JOIN (
    SELECT user_id, count(*) AS workspace_count
    FROM member
    GROUP BY user_id
) m ON m.user_id = u.id
WHERE sqlc.arg('query')::text = ''
   OR u.email ILIKE '%' || sqlc.arg('query') || '%'
   OR u.name ILIKE '%' || sqlc.arg('query') || '%'
ORDER BY u.created_at ASC, u.id ASC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CountAllUsers :one
SELECT count(*) FROM "user" u
WHERE sqlc.arg('query')::text = ''
   OR u.email ILIKE '%' || sqlc.arg('query') || '%'
   OR u.name ILIKE '%' || sqlc.arg('query') || '%';

-- name: ListAllWorkspaces :many
-- Instance-wide workspace directory for the admin workspace-management
-- page. Owner is the longest-standing owner-role member (multiple owners are
-- legal; the lateral picks one deterministically). member_count includes
-- every role.
SELECT w.*,
       u2.id AS owner_id, u2.name AS owner_name, u2.email AS owner_email,
       COALESCE(mc.member_count, 0) AS member_count
FROM workspace w
LEFT JOIN member m2 ON m2.workspace_id = w.id
    AND m2.role = 'owner'
    AND NOT EXISTS (
        SELECT 1 FROM member m3
        WHERE m3.workspace_id = m2.workspace_id
          AND m3.role = 'owner'
          AND (m3.created_at, m3.id) < (m2.created_at, m2.id)
    )
LEFT JOIN "user" u2 ON u2.id = m2.user_id
LEFT JOIN (
    SELECT workspace_id, count(*) AS member_count
    FROM member
    GROUP BY workspace_id
) mc ON mc.workspace_id = w.id
WHERE sqlc.arg('query')::text = ''
   OR w.name ILIKE '%' || sqlc.arg('query') || '%'
   OR w.slug ILIKE '%' || sqlc.arg('query') || '%'
ORDER BY w.created_at ASC, w.id ASC
LIMIT sqlc.arg('limit')
OFFSET sqlc.arg('offset');

-- name: CountAllWorkspaces :one
SELECT count(*) FROM workspace w
WHERE sqlc.arg('query')::text = ''
   OR w.name ILIKE '%' || sqlc.arg('query') || '%'
   OR w.slug ILIKE '%' || sqlc.arg('query') || '%';

-- name: RevokeAllPersonalAccessTokensByUser :many
-- Disable-side effect: every live PAT of the account dies with it. Returns
-- the hashes so the caller can drop the PATCache entries immediately
-- instead of waiting out AuthCacheTTL.
UPDATE personal_access_token
SET revoked = TRUE
WHERE user_id = $1 AND revoked = FALSE
RETURNING token_hash;

-- name: ListOpenImpersonationSessions :many
-- Impersonation sessions the given super admin opened and never closed
-- (no matching stop row) whose shadow token has not naturally expired.
-- Pairing rides the session_id both rows carry in metadata; the natural
-- expiry check reads the expires_at the start row stamped. Drives the
-- forced-termination audit written when the admin is disabled (P2-2).
SELECT started.target_id, started.metadata->>'session_id' AS session_id
FROM admin_audit_log AS started
WHERE started.actor_id = $1
  AND started.action = 'impersonation.start'
  AND started.metadata->>'session_id' IS NOT NULL
  AND (started.metadata->>'expires_at')::timestamptz > now()
  AND NOT EXISTS (
      SELECT 1 FROM admin_audit_log closed
      WHERE closed.actor_id = started.actor_id
        AND closed.action = 'impersonation.stop'
        AND closed.metadata->>'session_id' = started.metadata->>'session_id'
  );

-- name: CreateAdminAuditLog :exec
-- One row per instance-level admin action and per impersonation
-- session start/stop. Metadata carries action-specific context (e.g. the
-- before/after of a flag flip); never credentials or session content.
INSERT INTO admin_audit_log (actor_id, action, target_type, target_id, workspace_id, reason, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7);
