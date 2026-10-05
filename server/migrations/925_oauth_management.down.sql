-- RUYI-420 rollback: drop the OAuth management surface.
--
-- Dropping oauth_grant invalidates every access token that carries a grant
-- id (the gate can no longer resolve them) — that is the correct failure
-- direction for a rollback to pure-PAT-plus-legacy-JWT behaviour: legacy
-- tokens without the claim keep working until natural expiry, and any
-- client can re-authorize once the feature ships again. The oauth_client
-- lifecycle columns are plain data removal.

DROP INDEX IF EXISTS idx_oauth_grant_user;
DROP INDEX IF EXISTS idx_oauth_grant_client_user;
DROP TABLE IF EXISTS oauth_grant;

ALTER TABLE oauth_client DROP COLUMN IF EXISTS secret_updated_at;
ALTER TABLE oauth_client DROP COLUMN IF EXISTS disabled_by;
ALTER TABLE oauth_client DROP COLUMN IF EXISTS disabled_at;
