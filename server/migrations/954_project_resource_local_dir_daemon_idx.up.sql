-- RUYI-289: auto-discovery roots are the local_directory project resources
-- pinned to a daemon; the daemon_id lives inside the resource_ref JSONB. The
-- discovery plan is computed per daemon each knowledge cycle, so index the
-- extracted daemon_id for that one shape.
CREATE INDEX CONCURRENTLY idx_project_resource_local_dir_daemon
ON project_resource ((resource_ref->>'daemon_id'))
WHERE resource_type = 'local_directory';
