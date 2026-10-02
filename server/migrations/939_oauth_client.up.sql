-- RUYI-359 consolidation: absorbs 940 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 939.up.sql (RUYI-359 consolidation)

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
-- client_id uniqueness is enforced by the unique index below, not by an
-- inline UNIQUE: an inline constraint would build its index inside this
-- statement instead of as the separately reviewable DDL below.
CREATE TABLE IF NOT EXISTS oauth_client (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id TEXT NOT NULL,
    client_secret_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    redirect_uris TEXT[] NOT NULL DEFAULT '{}',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE oauth_client IS
    'Pre-registered OAuth clients for the MCP authorization server (RUYI-209). No DCR; rows are created by an operator.';
COMMENT ON COLUMN oauth_client.client_secret_hash IS
    'SHA-256 hash of the client secret. The plaintext is shown once at creation and never stored.';

-- >>> absorbed from 940.up.sql (RUYI-359 consolidation)

-- client_id is the lookup key of both /auth/oauth/authorize and
-- /auth/oauth/token, and two rows sharing one would make the secret check
-- ambiguous. Enforced in the database because an operator can create clients
-- through more than one round trip.
--
-- Inlined by RUYI-359 (oauth_client is created above in this same implicit
-- transaction, so the build does not need CONCURRENTLY).
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_client_client_id
    ON oauth_client (client_id);
