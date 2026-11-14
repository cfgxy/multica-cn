-- A disposable rollback cannot preserve an active lease after removing its
-- claim columns, so retain the audit row and make the physical write-back
-- explicitly failed before restoring the pre-claim state contract.
UPDATE knowledge_lifecycle_event
SET writeback_state = 'failed',
    writeback_error = 'write-back claim discarded by migration rollback',
    completed_at = COALESCE(completed_at, now())
WHERE writeback_state = 'in_progress';

UPDATE knowledge_entry
SET writeback_state = 'failed',
    writeback_error = 'write-back claim discarded by migration rollback'
WHERE writeback_state = 'in_progress';

ALTER TABLE knowledge_lifecycle_event
    DROP CONSTRAINT IF EXISTS knowledge_lifecycle_event_writeback_claim_check,
    DROP CONSTRAINT IF EXISTS knowledge_lifecycle_event_writeback_state_check,
    DROP COLUMN IF EXISTS writeback_claim_token,
    DROP COLUMN IF EXISTS writeback_claimed_at,
    DROP COLUMN IF EXISTS writeback_claimed_by;

ALTER TABLE knowledge_lifecycle_event
    ADD CONSTRAINT knowledge_lifecycle_event_writeback_state_check
    CHECK (writeback_state IN ('not_requested', 'queued', 'succeeded', 'failed', 'cancelled'));

ALTER TABLE knowledge_entry
    DROP CONSTRAINT IF EXISTS knowledge_entry_writeback_state_check;

ALTER TABLE knowledge_entry
    ADD CONSTRAINT knowledge_entry_writeback_state_check
    CHECK (writeback_state IN ('not_requested', 'queued', 'succeeded', 'failed', 'cancelled'));
