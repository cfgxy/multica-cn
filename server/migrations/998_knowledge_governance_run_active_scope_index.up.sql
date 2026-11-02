CREATE UNIQUE INDEX CONCURRENTLY uidx_knowledge_governance_run_active_scope
ON knowledge_governance_run (workspace_id, dir_id)
WHERE status = 'running';
