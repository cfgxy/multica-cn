-- Add `updated_at` to task_usage so the daily-rollup worker (added in 073)
-- can detect rows that were corrected by `UpsertTaskUsage` after their
-- original creation. The existing UPSERT path overwrites token counts on
-- conflict but leaves created_at unchanged, so a watermark on created_at
-- alone would silently miss those corrections.
--
-- Backfilled to created_at for existing rows. Application code (the
-- regenerated UpsertTaskUsage in this PR) sets it explicitly on conflict.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE task_usage SET updated_at = created_at WHERE updated_at > created_at;
