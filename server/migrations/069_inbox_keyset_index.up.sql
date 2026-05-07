-- Composite partial index backing cursor-paginated inbox listing. The query
-- shape is:
--   WHERE workspace_id=$1 AND recipient_type=$2 AND recipient_id=$3
--     AND archived=false AND (created_at, id) < ($4, $5)
--   ORDER BY created_at DESC, id DESC LIMIT $6
-- With (workspace_id, recipient_type, recipient_id, created_at DESC, id DESC)
-- under the archived=false predicate, the planner can serve this as an
-- index-only scan with no sort.
--
-- The pre-existing idx_inbox_recipient (recipient_type, recipient_id, read)
-- only covered the per-recipient unread-count query; it cannot drive the
-- workspace-scoped keyset pagination, so we add a new index rather than
-- replace it.
--
-- Not using CREATE INDEX CONCURRENTLY here for the same reason 068
-- (timeline keyset) didn't: at current scale (early-stage product, 2–10
-- person workspaces) the build is sub-second and the brief share-lock is
-- acceptable. When inbox_item grows past ~1M rows the lock window will
-- matter — at that point revisit ALL keyset indexes (068 + 069) together
-- and either rebuild via CONCURRENTLY in an ops one-off or tighten the
-- migration runner to support non-transactional migrations. Don't fix
-- one in isolation; it just creates inconsistency.

CREATE INDEX IF NOT EXISTS idx_inbox_active_keyset
    ON inbox_item (workspace_id, recipient_type, recipient_id, created_at DESC, id DESC)
    WHERE archived = false;
