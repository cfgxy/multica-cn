-- RUYI-889: knowledge entries remain authoritative in their bound daemon's bd
-- directory. This durable queue records owner-confirmed mutations until that
-- daemon writes and reads them back; the server updates its mirror only from
-- the reported success.
CREATE TABLE knowledge_entry_operation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    entry_id UUID NOT NULL,
    operation_type TEXT NOT NULL CHECK (operation_type IN ('edit', 'delete', 'merge')),
    content TEXT NOT NULL DEFAULT '',
    -- For merge, these source entry ids are deleted after the target content
    -- was written and read back. Edit and delete use an empty array.
    delete_entry_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    requested_by UUID,
    -- queued | completed | failed. Only a daemon result resolves queued work.
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'completed', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

COMMENT ON TABLE knowledge_entry_operation IS 'Daemon-executed edit, delete and merge jobs for ultimate knowledge entries; mirror changes land only after bd read-back.';
