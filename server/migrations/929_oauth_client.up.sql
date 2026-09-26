-- Pre-registered OAuth clients for the MCP authorization server (RUYI-209).
--
-- The authorization-code flow is deliberately minimal: this is the ONLY new
-- persistent entity. Authorization codes live in Redis (TTL 60s, single-use),
-- the signing key comes from OAUTH_SIGNING_KEY, and the first version issues
-- no refresh tokens — so neither needs a table. See
-- docs/adr/001-mcp-oauth-behind-nextjs-proxy.md §3.7.
--
-- No foreign key on created_by: the repository forbids database-level foreign
-- keys, so the owning user is resolved in application code. A deleted user
-- leaves the row behind; the client stays usable until an operator removes it,
-- which is the coarse-grained revocation the first version ships with.
--
-- client_secret_hash stores only the hash. The plaintext secret is returned
-- once at creation time and never persisted or logged.
--
-- client_id uniqueness is enforced by 925, not by an inline UNIQUE: the house
-- rule requires every index a migration creates to be built CONCURRENTLY, and
-- an inline constraint would build its index inside this statement.
CREATE TABLE IF NOT EXISTS oauth_clients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id TEXT NOT NULL,
    client_secret_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    redirect_uris TEXT[] NOT NULL DEFAULT '{}',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE oauth_clients IS
    'Pre-registered OAuth clients for the MCP authorization server (RUYI-209). No DCR; rows are created by an operator.';
COMMENT ON COLUMN oauth_clients.client_secret_hash IS
    'SHA-256 hash of the client secret. The plaintext is shown once at creation and never stored.';
