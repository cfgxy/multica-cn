-- Index for the admin audit trail. Kept in its own single-statement file
-- because CREATE INDEX CONCURRENTLY cannot run inside a transaction next to
-- the other 376 statements.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_admin_audit_log_actor_created
    ON admin_audit_log (actor_id, created_at DESC);
