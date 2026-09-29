-- RUYI-289: the daemon pull loop asks for every knowledge directory assigned
-- to it (plus unclaimed ones) each cycle; this index keeps that lookup off a
-- sequential scan of knowledge_dir.
CREATE INDEX CONCURRENTLY idx_knowledge_dir_daemon
ON knowledge_dir (daemon_id);
