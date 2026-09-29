-- RUYI-289: every newly discovered knowledge source whose first scan finds
-- entries generates exactly one system proposal into the pool. The dir id is
-- recorded inside generation_snapshot; this partial unique index makes the
-- "one proposal per directory, ever" rule database-enforced, so a daemon
-- re-report or a server retry cannot mint a second proposal for the same
-- source. Human/member proposals never carry the key and are unaffected.
CREATE UNIQUE INDEX CONCURRENTLY uidx_proposal_system_dir
ON proposal ((generation_snapshot->>'knowledge_dir_id'))
WHERE generation_snapshot ? 'knowledge_dir_id';
