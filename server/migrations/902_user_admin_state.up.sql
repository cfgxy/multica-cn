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
