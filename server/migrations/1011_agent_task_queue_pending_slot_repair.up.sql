-- Repair installations whose migration ledger recorded the v2 index although
-- the index was never created (or only the older queued/dispatched predicate
-- remains). Use a new name so a stale/invalid v2 catalog entry cannot suppress
-- this repair. The predicate is the authoritative single pending-slot contract.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_one_pending_task_per_issue_agent_v3
    ON agent_task_queue (issue_id, agent_id)
    WHERE status IN ('queued', 'dispatched')
       OR (status = 'deferred' AND context->>'channel_issue_media_pending' = 'true');
