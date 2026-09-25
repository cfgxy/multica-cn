-- Run-level prompt version attribution (RUYI-183, self-evolution phase 1).
--
-- Records, at task-claim time, which prompt_version.version number of each
-- injected tier (workspace/project/squad/agent) this run actually executed
-- with. A tier that was not injected for this run is simply absent from the
-- object — never written as version 0 — so "not applicable" and "version
-- zero" stay distinguishable.
--
-- Shape: {"workspace": <int>, "project": <int>, "squad": <int>, "agent": <int>}
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS prompt_versions JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN agent_task_queue.prompt_versions IS
    'Prompt tier version numbers this run was claimed with (RUYI-183). Keys present only for tiers actually injected; absent, not zero, for tiers that were not.';
