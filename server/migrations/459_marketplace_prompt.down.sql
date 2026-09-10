ALTER TABLE squad DROP COLUMN IF EXISTS marketplace_prompt_state;
ALTER TABLE agent DROP COLUMN IF EXISTS marketplace_prompt_state;
DROP TABLE IF EXISTS workspace_prompt_install;
DROP TABLE IF EXISTS marketplace_prompt_version;
