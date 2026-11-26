CREATE UNIQUE INDEX CONCURRENTLY idx_knowledge_governance_opinion_active_entry
ON knowledge_governance_opinion (workspace_id, entry_id)
WHERE status IN ('pending', 'approved', 'applying');
