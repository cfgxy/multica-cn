-- The directory read is "every public package in this instance", which without
-- an index is a sequential scan of every package ever published, on a page any
-- administrator can open.
--
-- Partial on visibility = 'public': private packages are the majority and are
-- never reached by this query — they are read by (workspace_id, plugin_key),
-- which idx_plugin_package_workspace_key already covers.
--
-- Own file: CREATE INDEX CONCURRENTLY cannot run inside a transaction or share
-- a multi-command string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_plugin_package_public
    ON plugin_package (name ASC)
    WHERE visibility = 'public';
