-- Same reason as 927: the only key on prompt_perplexity_score is the scope
-- tuple, and workspace teardown deletes by workspace_id.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_prompt_perplexity_score_workspace
    ON prompt_perplexity_score (workspace_id);
