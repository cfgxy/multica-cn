-- Down for 912_issue_run_suppressed, renumbered from the former migration 949 by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.

ALTER TABLE issue DROP COLUMN run_suppressed_at;
ALTER TABLE issue DROP COLUMN run_suppressed;
