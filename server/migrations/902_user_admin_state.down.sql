-- Reverse of 902_user_admin_state.up.sql (renumbered from 441, RUYI-75).
DROP TABLE IF EXISTS admin_audit_log;
ALTER TABLE "user" DROP COLUMN IF EXISTS disabled_by;
ALTER TABLE "user" DROP COLUMN IF EXISTS disabled_at;
ALTER TABLE "user" DROP COLUMN IF EXISTS is_super_admin;
