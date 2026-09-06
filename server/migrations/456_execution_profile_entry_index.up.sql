-- One entry per agent per profile. The entry upsert path relies on this index
-- for its ON CONFLICT target, so it is a hard requirement rather than a
-- lookup optimisation. Own file: see 455.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_execution_profile_entry_profile_agent
    ON execution_profile_entry (profile_id, agent_id);
