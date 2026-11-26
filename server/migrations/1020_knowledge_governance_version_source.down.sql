ALTER TABLE knowledge_entry_version DROP CONSTRAINT IF EXISTS knowledge_entry_version_source_check;
ALTER TABLE knowledge_entry_version ADD CONSTRAINT knowledge_entry_version_source_check
    CHECK (source IN ('initial', 'scan', 'restore'));
