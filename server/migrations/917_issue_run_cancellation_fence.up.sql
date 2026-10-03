-- RUYI-384: an issue in the cancelled category accepts no new agent runs.
--
-- This is the run-side counterpart of lock_task_owner_rows (migration 284).
-- That fence serializes task writes against workspace teardown and the legacy
-- runtime merge; this predicate serializes them against issue cancellation:
--
--   * FOR SHARE on the issue row conflicts with the NO KEY UPDATE an issue
--     status write takes. FOR KEY SHARE (the 284 strength) would NOT — a
--     cancellation and an enqueue could both sail past each other, and a task
--     inserted after the cancellation's cascade scan would sit queued on a
--     cancelled issue forever.
--   * So an enqueue either commits before the cancellation (the cascade then
--     sees and cancels the new row — the cascade reads after the issue write
--     committed) or it blocks, re-reads the committed status, and refuses.
--     The check and the insert are one statement, so no third interleaving
--     exists.
--
-- Call it from the WHERE clause of every statement that inserts a task row
-- carrying an issue_id, next to lock_task_owner_rows:
--
--     INSERT INTO agent_task_queue (...)
--     SELECT ... WHERE lock_task_owner_rows($agent, $issue, $runtime)
--                    AND issue_accepts_runs($issue)
--
-- VOLATILE (the default) is load-bearing, exactly as for lock_task_owner_rows:
-- it stops the planner from inlining the body or eliding the lock.
--
-- Returns TRUE when the issue row is gone: lock_task_owner_rows refuses that
-- write already, and double-reporting a missing row would blur which fence
-- fired. Chat tasks (issue_id NULL) never reach the cancellation scope and
-- return TRUE without touching the issue row.

CREATE OR REPLACE FUNCTION issue_accepts_runs(p_issue_id uuid)
RETURNS boolean
LANGUAGE plpgsql
AS $$
DECLARE
    v_accepts boolean;
BEGIN
    IF p_issue_id IS NULL THEN
        RETURN TRUE;
    END IF;

    SELECT issue_effective_status(i.workspace_id, i.status) <> 'cancelled'
      INTO v_accepts
      FROM issue i
     WHERE i.id = p_issue_id
       FOR SHARE;

    IF v_accepts IS NULL THEN
        RETURN TRUE;
    END IF;
    RETURN v_accepts;
END;
$$;
