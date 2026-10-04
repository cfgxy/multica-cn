-- RUYI-420: OAuth management control plane (System Settings).
--
-- Two changes, one atomic deliverable:
--   1. oauth_client grows the lifecycle columns the management UI needs:
--      soft-disable state (disabled_at/disabled_by) and the timestamp the
--      current secret was written (secret_updated_at), so the UI can show
--      "secret rotated at" without exposing the hash itself.
--   2. New oauth_grant table: one row per (client, user) authorization.
--      This is the revocation anchor — access tokens now carry the grant id
--      and middleware.Auth consults this row's state after verifying the
--      JWT signature, so revoke/disable takes effect within the gate cache
--      window instead of at the 90-day token expiry.
--
-- No foreign keys, per repo rule: client_id references oauth_client.client_id
-- and user_id references "user".id in the application layer only. Deleting a
-- client keeps its grant rows (the handler revokes them first so the audit
-- trail and the user-facing "my grants" page keep their history).
--
-- Indexes are inlined in this same statement batch rather than built
-- CONCURRENTLY: the table is brand new inside this one implicit transaction,
-- so no concurrent reader can observe an unconstrained build window — same
-- rationale as 909's idx_oauth_client_client_id after the RUYI-359
-- consolidation.

ALTER TABLE oauth_client ADD COLUMN disabled_at TIMESTAMPTZ;
ALTER TABLE oauth_client ADD COLUMN disabled_by UUID;
ALTER TABLE oauth_client ADD COLUMN secret_updated_at TIMESTAMPTZ;

COMMENT ON COLUMN oauth_client.disabled_at IS
    'Soft-disable timestamp. NULL = enabled. A disabled client cannot mint new tokens and its existing tokens fail the grant gate within the cache window.';
COMMENT ON COLUMN oauth_client.disabled_by IS
    'User who set disabled_at. No DB foreign key, resolved in application code.';
COMMENT ON COLUMN oauth_client.secret_updated_at IS
    'When the current client_secret_hash was written. NULL = original create-time secret. Shown as "rotated at"; never the hash itself.';

CREATE TABLE IF NOT EXISTS oauth_grant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id TEXT NOT NULL,
    user_id UUID NOT NULL,
    scope TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

COMMENT ON TABLE oauth_grant IS
    'One row per (client, user) OAuth authorization (RUYI-420). Access tokens carry the grant id; middleware.Auth checks revoked_at/client state after signature verification, making revocation effective within the gate cache TTL. scope stores the consented scope string; the legacy value "mcp" means full MCP access.';
COMMENT ON COLUMN oauth_grant.last_used_at IS
    'Refreshed at most once per gate-cache TTL window per grant, mirroring the PAT last_used_at throttle.';

-- The lookup key for "does this client already have a grant for this user"
-- at authorize time and for the client-detail grant list.
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_grant_client_user
    ON oauth_grant (client_id, user_id);

-- The "my authorizations" page lists a user's grants across all clients.
CREATE INDEX IF NOT EXISTS idx_oauth_grant_user
    ON oauth_grant (user_id);
