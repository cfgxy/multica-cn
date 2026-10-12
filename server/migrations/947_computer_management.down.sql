-- RUYI-670: reverse of 947_computer_management (trustbe-cn 536,538-540,545,546,554-556).
-- Concurrently-built indexes from 948-959 are dropped by their own down files first.

-- fork 556 reverse
ALTER TABLE computer_runtime_model_config DROP COLUMN IF EXISTS last_save_operation_id;
-- fork 555 reverse
ALTER TABLE model_key_source DROP COLUMN IF EXISTS retry_after;
-- fork 554 reverse
ALTER TABLE computer_runtime_model_config DROP COLUMN IF EXISTS last_test_result;
ALTER TABLE computer_runtime_model_config DROP COLUMN IF EXISTS file_manifest;
ALTER TABLE user_model_key_grant DROP COLUMN IF EXISTS api_key_name;
-- fork 546 reverse
DROP TABLE IF EXISTS computer_runtime_model_config;
DROP TABLE IF EXISTS runtime_config_template;
DROP TABLE IF EXISTS user_model_key_grant;
DROP TABLE IF EXISTS model_key_directory_entry;
DROP TABLE IF EXISTS model_key_source;
-- fork 545 reverse
ALTER TABLE computer_binding DROP COLUMN IF EXISTS daemon_checked_at, DROP COLUMN IF EXISTS daemon_state;
-- fork 540 reverse
ALTER TABLE computer_binding DROP COLUMN IF EXISTS checked_at, DROP COLUMN IF EXISTS account_state, DROP COLUMN IF EXISTS archived_at;
DROP TABLE IF EXISTS computer_runtime_asset;
DROP TABLE IF EXISTS computer_operation;
-- fork 539 reverse
ALTER TABLE computer DROP COLUMN IF EXISTS checked_at;
ALTER TABLE computer DROP COLUMN IF EXISTS check_ok;
ALTER TABLE computer DROP COLUMN IF EXISTS check_detail;
-- fork 538 reverse
ALTER TABLE computer_audit DROP COLUMN IF EXISTS computer_id;
-- fork 536 reverse
DROP TABLE IF EXISTS computer_audit;
DROP TABLE IF EXISTS computer_binding;
DROP TABLE IF EXISTS computer_credential;
DROP TABLE IF EXISTS computer;
DROP SEQUENCE IF EXISTS computer_health_port_seq;
