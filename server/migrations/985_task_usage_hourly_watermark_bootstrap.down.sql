-- The bootstrap only advances an existing watermark. Reverting it would make
-- already-processed usage appear unprocessed, so the rollback is intentionally
-- a no-op.
SELECT 1;
