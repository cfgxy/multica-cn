CREATE INDEX CONCURRENTLY idx_knowledge_governance_finding_run
ON knowledge_governance_finding (run_id, created_at);
