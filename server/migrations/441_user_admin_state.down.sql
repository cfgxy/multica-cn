-- Reverse of 376_user_admin_state.up.sql.
DROP TABLE IF EXISTS admin_audit_log;
ALTER TABLE "user" DROP COLUMN IF EXISTS disabled_by;
ALTER TABLE "user" DROP COLUMN IF EXISTS disabled_at;
ALTER TABLE "user" DROP COLUMN IF EXISTS is_super_admin;
