-- RUYI-940: an empty session_id is an explicit "attribution unknown" state.
-- Historical v2 rows and old daemon payloads must remain in Issue totals, but
-- cannot seed any session's cumulative baseline without evidence.
ALTER TABLE task_usage
    ADD COLUMN IF NOT EXISTS session_attribution_known BOOLEAN NOT NULL DEFAULT FALSE;
