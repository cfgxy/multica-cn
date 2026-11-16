-- The migration changes cumulative snapshots into deltas and cannot reconstruct
-- the discarded prior snapshots. Rolling back only removes the marker; it does
-- not restore the historical, incorrect cumulative representation.
ALTER TABLE task_usage
    DROP COLUMN IF EXISTS usage_semantics_version;
