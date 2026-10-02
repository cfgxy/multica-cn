-- RUYI-359 consolidation: absorbs 903 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 903.down.sql (RUYI-359 consolidation)

-- Reverse of the admin audit index (renumbered from 442, RUYI-75); the DROP is
-- non-concurrent because it shares this file's implicit transaction with the
-- table drop below.
DROP INDEX IF EXISTS idx_admin_audit_log_actor_created;

-- >>> lead 902.down.sql (drops the structures created above)

-- Reverse of 902_user_admin_state.up.sql (renumbered from 441, RUYI-75).
DROP TABLE IF EXISTS admin_audit_log;
ALTER TABLE "user" DROP COLUMN IF EXISTS disabled_by;
ALTER TABLE "user" DROP COLUMN IF EXISTS disabled_at;
ALTER TABLE "user" DROP COLUMN IF EXISTS is_super_admin;
