ALTER TABLE knowledge_entry
    DROP CONSTRAINT IF EXISTS knowledge_entry_writeback_state_check;

ALTER TABLE knowledge_entry
    ADD CONSTRAINT knowledge_entry_writeback_state_check
    CHECK (writeback_state IN ('not_requested', 'queued', 'in_progress', 'succeeded', 'failed', 'cancelled'));

ALTER TABLE knowledge_lifecycle_event
    DROP CONSTRAINT IF EXISTS knowledge_lifecycle_event_writeback_state_check;

ALTER TABLE knowledge_lifecycle_event
    ADD CONSTRAINT knowledge_lifecycle_event_writeback_state_check
    CHECK (writeback_state IN ('not_requested', 'queued', 'in_progress', 'succeeded', 'failed', 'cancelled'));

ALTER TABLE knowledge_lifecycle_event
    ADD COLUMN writeback_claimed_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN writeback_claimed_at TIMESTAMPTZ,
    ADD COLUMN writeback_claim_token UUID;

ALTER TABLE knowledge_lifecycle_event
    ADD CONSTRAINT knowledge_lifecycle_event_writeback_claim_check
    CHECK (
        writeback_state <> 'in_progress'
        OR (writeback_claimed_by <> '' AND writeback_claimed_at IS NOT NULL AND writeback_claim_token IS NOT NULL)
    );
