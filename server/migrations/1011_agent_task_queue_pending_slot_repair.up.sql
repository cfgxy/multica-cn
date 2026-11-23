-- A pre-existing v3 name (including an invalid concurrent-index remnant) is
-- deliberately a hard migration failure rather than a silent IF NOT EXISTS
-- skip. The runner will not record a failed migration in schema_migrations;
-- remove that remnant with DROP INDEX CONCURRENTLY and re-run migration 1011.
CREATE UNIQUE INDEX CONCURRENTLY idx_one_pending_task_per_issue_agent_v3
    ON agent_task_queue (issue_id, agent_id)
    WHERE status IN ('queued', 'dispatched')
       OR (status = 'deferred' AND context->>'channel_issue_media_pending' = 'true');
