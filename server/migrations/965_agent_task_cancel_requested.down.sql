-- Converge any in-flight cancel_requested rows before shrinking the CHECK, so
-- the constraint swap cannot fail on live data. They are mid-cancellation by
-- definition; cancelled is the honest terminal summary once the old code no
-- longer knows how to confirm the stop.
UPDATE agent_task_queue SET status = 'cancelled', completed_at = COALESCE(completed_at, now())
WHERE status = 'cancel_requested';

ALTER TABLE agent_task_queue
    DROP CONSTRAINT agent_task_queue_status_check,
    ADD CONSTRAINT agent_task_queue_status_check
        CHECK (status IN (
            'queued',
            'dispatched',
            'running',
            'completed',
            'failed',
            'cancelled',
            'waiting_local_directory',
            'deferred'
        ));
