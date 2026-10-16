CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS issue_coordinator_session_identity_uidx
    ON issue_coordinator_session (issue_id, leader_agent_id);
