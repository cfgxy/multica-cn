-- RUYI-289: auto-discovery now registers candidate directories from daemons
-- instead of the single server process, so the (workspace_id, path) duplicate
-- check that used to be an application-level count query needs a database
-- backstop: two daemons discovering the same directory concurrently must not
-- produce two rows. Unregistered (removed) paths may re-register, hence the
-- partial index.
CREATE UNIQUE INDEX CONCURRENTLY uidx_knowledge_dir_ws_path
ON knowledge_dir (workspace_id, path)
WHERE removed = FALSE;
