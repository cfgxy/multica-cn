-- Provenance backfill and immutable history extension are intentionally irreversible.
-- This down migration is a no-op so rollback does not delete or overwrite history.
SELECT 1;
