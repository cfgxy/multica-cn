ALTER TABLE workspace DROP COLUMN IF EXISTS active_execution_profile_id;
DROP TABLE IF EXISTS execution_profile_entry;
DROP TABLE IF EXISTS execution_profile;
