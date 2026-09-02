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
ALTER TABLE "user" ADD COLUMN is_super_admin BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE "user" ADD COLUMN disabled_at TIMESTAMPTZ;
ALTER TABLE "user" ADD COLUMN disabled_by UUID;

CREATE TABLE admin_audit_log (
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
