-- RUYI-552 direction 3: the daily retrospective executes as one round of the
-- configured agent's run — the agent reads the window's completed issues with
-- its own tools and reports improvement drafts back; no issue is ever created
-- and no issue ever receives a comment. This replaces the direction-2 direct
-- LLM columns (934, never released) with an agent selection.
--
-- retrospective_config.agent_id is that selection; updated_by records the
-- member who last saved the config, so the triggered run carries an honest
-- human originator instead of an unattributed one.
-- retrospective_run.task_id links the run to the platform task enqueued for
-- it, so terminal task states (failed / cancelled / queued-expired) reconcile
-- back onto the run row instead of leaving it "running" forever.
ALTER TABLE retrospective_config ADD COLUMN agent_id UUID;
ALTER TABLE retrospective_config ADD COLUMN updated_by UUID;
ALTER TABLE retrospective_run ADD COLUMN task_id UUID;

COMMENT ON COLUMN retrospective_config.agent_id IS
    'Agent the retrospective executes as: one run of this agent reviews the window''s completed issues and reports drafts; the run never creates an issue or comments.';
COMMENT ON COLUMN retrospective_config.updated_by IS
    'Member who last saved this config; the honest human originator attributed to the runs it triggers.';
COMMENT ON COLUMN retrospective_run.task_id IS
    'Platform task enqueued for this run (agent_task_queue.id); terminal task states reconcile onto the run row through it.';
