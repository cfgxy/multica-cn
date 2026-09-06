-- Index for the admin audit trail. Kept in its own single-statement file
-- because CREATE INDEX CONCURRENTLY cannot run inside a transaction next to
-- the other statements.
--
-- Renumbered 442 → 453 (RUYI-75): prefix 442 collides with the upstream
-- 442_vcs_reference_only_repair. Already idempotent (IF NOT EXISTS), so
-- databases that applied the old 442 stem re-run this as 453 harmlessly.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_admin_audit_log_actor_created
    ON admin_audit_log (actor_id, created_at DESC);
