-- Repair legacy installations where migration 127 was recorded before the
-- reference_only column was actually added to issue_pull_request. The recorded
-- version prevents the original migration from being replayed.
ALTER TABLE issue_pull_request
    ADD COLUMN IF NOT EXISTS reference_only BOOLEAN NOT NULL DEFAULT FALSE;
