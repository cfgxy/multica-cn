CREATE INDEX CONCURRENTLY idx_proposal_workspace_status
ON proposal (workspace_id, status);
