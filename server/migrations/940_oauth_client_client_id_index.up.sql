-- client_id is the lookup key of both /auth/oauth/authorize and
-- /auth/oauth/token, and two rows sharing one would make the secret check
-- ambiguous. Enforced in the database because an operator can create clients
-- through more than one round trip.
--
-- Own file: CREATE UNIQUE INDEX CONCURRENTLY cannot run inside a transaction
-- or share a multi-command string.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_oauth_client_client_id
    ON oauth_client (client_id);
