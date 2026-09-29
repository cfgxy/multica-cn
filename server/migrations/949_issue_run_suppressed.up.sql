-- RUYI-275: read-side snapshot of an honored `suppress_run`.
--
-- activity_log keeps the append-only audit trail (one `run_suppressed` event
-- per honored suppress); these columns carry the *current* state so the board
-- and list views can render the "on hold" badge without querying the event
-- history per card. Cleared by the next write that truly starts a run or by
-- the assignee being removed; untouched by writes that start no run.
ALTER TABLE issue ADD COLUMN run_suppressed boolean NOT NULL DEFAULT false;
ALTER TABLE issue ADD COLUMN run_suppressed_at timestamptz;
