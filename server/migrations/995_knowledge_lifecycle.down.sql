DROP TABLE IF EXISTS knowledge_lifecycle_event;
DROP TABLE IF EXISTS knowledge_entry_version;

ALTER TABLE knowledge_entry
    DROP COLUMN IF EXISTS writeback_error,
    DROP COLUMN IF EXISTS writeback_state,
    DROP COLUMN IF EXISTS replacement_entry_id,
    DROP COLUMN IF EXISTS missing_confirmation,
    DROP COLUMN IF EXISTS current_version,
    DROP COLUMN IF EXISTS lifecycle_state;

ALTER TABLE knowledge_dir DROP COLUMN IF EXISTS managed_active;
