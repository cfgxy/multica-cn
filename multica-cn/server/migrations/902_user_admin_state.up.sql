-- RUYI-359 consolidation: absorbs 903 into this file
-- (previously separate single-statement migrations; stems retired). Statement
-- bodies are unchanged except CREATE/DROP INDEX lost the CONCURRENTLY keyword,
-- which is safe because every index target is created/altered in this same
-- file (914 precedent) and the whole file runs as one implicit transaction.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.


-- >>> absorbed from 902.up.sql (RUYI-359 consolidation)

-- Super-admin account management (RUYI-47). Adds the persisted account
-- state that replaces the hardcoded temporary ban list, plus the
-- instance-level admin audit trail.
--
-- is_super_admin is a flat boolean, not an RBAC table: the feature needs a
-- single instance-level privilege and the evaluation ruled out carrying an
-- instance role model for it.
--
-- disabled_at / disabled_by follow the "who acted, when" pattern: NULL means
-- enabled; a timestamp means disabled, with disabled_by naming the admin who
-- acted. Re-enabling clears both in the same UPDATE so the row always shows
-- the current state and its actor together.
--
-- No foreign keys by repo convention; actor/target integrity is enforced in
-- application code.
--
-- Renumbered 441 → 452 (RUYI-75): prefix 441 collides with the upstream
-- 441_runtime_profile_add_codearts. DDL is idempotent so databases that
-- already applied the old 441 stem re-run this as 452 harmlessly.
ALTER TABLE "user" ADD COLUMN IF NOT EXISTS is_super_admin BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE "user" ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ;
ALTER TABLE "user" ADD COLUMN IF NOT EXISTS disabled_by UUID;

CREATE TABLE IF NOT EXISTS admin_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID NOT NULL,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id UUID,
    workspace_id UUID,
    reason TEXT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- >>> absorbed from 903.up.sql (RUYI-359 consolidation)

-- Index for the admin audit trail (absorbed by RUYI-359; built without
-- CONCURRENTLY because admin_audit_log is created above in this same
-- implicit transaction — 914 precedent).
--
-- Renumbered 442 → 453 (RUYI-75): prefix 442 collides with the upstream
-- 442_vcs_reference_only_repair. Already idempotent (IF NOT EXISTS).
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_actor_created
    ON admin_audit_log (actor_id, created_at DESC);
