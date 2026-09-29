-- RUYI-289: the ultimate knowledge directory is now auto-designated by
-- daemons (bd init + register on a daemon-managed path) instead of only by
-- the Owner through the management channel. Multiple daemons can race to
-- designate one for the same workspace; the existing single-ultimate rule
-- becomes a database constraint so exactly one registration wins and the
-- losers get a conflict instead of a second row.
CREATE UNIQUE INDEX CONCURRENTLY uidx_knowledge_dir_ultimate_active
ON knowledge_dir (workspace_id)
WHERE kind = 'ultimate' AND removed = FALSE;
