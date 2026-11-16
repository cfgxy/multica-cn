ALTER TABLE knowledge_entry_version
    DROP CONSTRAINT IF EXISTS knowledge_entry_version_source_check;

ALTER TABLE knowledge_entry_version
    ADD CONSTRAINT knowledge_entry_version_source_check
    CHECK (source IN ('initial', 'scan', 'restore', 'adoption', 'operation'));

-- Deployed legacy mirrors can have a current_version without its immutable row.
-- Backfill only the missing current row and preserve every existing history row.
INSERT INTO knowledge_entry_version (
    workspace_id,
    entry_id,
    version,
    content,
    content_sha256,
    source,
    created_by
)
SELECT e.workspace_id,
       e.id,
       e.current_version,
       e.content,
       e.content_sha256,
       'initial',
       NULL
FROM knowledge_entry e
WHERE NOT EXISTS (
    SELECT 1
    FROM knowledge_entry_version v
    WHERE v.entry_id = e.id
      AND v.workspace_id = e.workspace_id
      AND v.version = e.current_version
);
