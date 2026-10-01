CREATE INDEX CONCURRENTLY idx_prompt_proposal_workspace_status
ON prompt_proposal (workspace_id, status, created_at DESC);
