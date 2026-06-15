-- Reverse 120_runtime_profile.up.sql. Order matters: drop the partial index
-- and the profile_id column (which carries the FK into runtime_profile) before
-- dropping the table the FK points at.

DROP INDEX IF EXISTS agent_runtime_workspace_daemon_profile_key;

ALTER TABLE agent_runtime
    DROP COLUMN IF EXISTS profile_id;

DROP INDEX IF EXISTS idx_runtime_profile_workspace;

DROP TABLE IF EXISTS runtime_profile;
