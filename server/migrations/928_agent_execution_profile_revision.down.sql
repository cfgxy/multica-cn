ALTER TABLE agent
    DROP COLUMN IF EXISTS revision;
ALTER TABLE execution_profile
    DROP COLUMN IF EXISTS revision;
