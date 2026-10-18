-- RUYI-690 down: drop the suspension dimension. Row history is not preserved
-- — the feature is being removed wholesale, and a suspended-issue snapshot
-- column without its audit table would be uninterpretable.
DROP INDEX IF EXISTS idx_issue_suspension_workspace_status;
DROP INDEX IF EXISTS uidx_issue_suspension_active_per_issue;
DROP TABLE IF EXISTS issue_suspension;
ALTER TABLE issue DROP COLUMN IF EXISTS suspended_at;
