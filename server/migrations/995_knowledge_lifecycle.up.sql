ALTER TABLE knowledge_dir
    ADD COLUMN managed_active BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE knowledge_entry
    ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active', 'needs_review', 'superseded', 'deprecated', 'invalid', 'duplicate')),
    ADD COLUMN current_version INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN missing_confirmation INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN replacement_entry_id UUID,
    ADD COLUMN writeback_state TEXT NOT NULL DEFAULT 'not_requested'
        CHECK (writeback_state IN ('not_requested', 'queued', 'succeeded', 'failed', 'cancelled')),
    ADD COLUMN writeback_error TEXT NOT NULL DEFAULT '';

CREATE TABLE knowledge_entry_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    entry_id UUID NOT NULL,
    version INTEGER NOT NULL,
    content TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('initial', 'scan', 'restore')),
    scan_batch_id UUID,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE knowledge_lifecycle_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    entry_id UUID NOT NULL,
    from_state TEXT NOT NULL DEFAULT '',
    to_state TEXT NOT NULL,
    operation TEXT NOT NULL CHECK (operation IN ('transition', 'restore_candidate', 'source_deleted', 'source_restored', 'writeback_remove', 'writeback_restore')),
    reason TEXT NOT NULL DEFAULT '',
    evidence_ref TEXT NOT NULL CHECK (btrim(evidence_ref) <> ''),
    replacement_entry_id UUID,
    version_id UUID,
    expected_content_sha256 TEXT NOT NULL DEFAULT '',
    writeback_state TEXT NOT NULL DEFAULT 'not_requested'
        CHECK (writeback_state IN ('not_requested', 'queued', 'succeeded', 'failed', 'cancelled')),
    writeback_error TEXT NOT NULL DEFAULT '',
    actor_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

INSERT INTO knowledge_entry_version (
    workspace_id, entry_id, version, content, content_sha256, source, created_at
)
SELECT workspace_id, id, 1, content, content_sha256, 'initial', first_seen_at
FROM knowledge_entry;
