-- Per-runtime call-count admission budget. This deliberately belongs on the
-- runtime rather than daemon_backpressure_settings: host /proc memory, swap,
-- and PSI sampling is a daemon-host concern; provider billing is a runtime
-- concern and is derived exclusively from server-persisted task usage.
ALTER TABLE agent_runtime
    ADD COLUMN IF NOT EXISTS model_call_budget_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS model_call_budget_max_calls BIGINT NOT NULL DEFAULT 1000,
    ADD COLUMN IF NOT EXISTS model_call_budget_window_seconds INTEGER NOT NULL DEFAULT 86400;

COMMENT ON COLUMN agent_runtime.model_call_budget_enabled IS
    'Whether complete known provider-call usage may hold new task claims for this runtime.';
COMMENT ON COLUMN agent_runtime.model_call_budget_max_calls IS
    'Maximum complete known provider calls allowed in the rolling budget window.';
COMMENT ON COLUMN agent_runtime.model_call_budget_window_seconds IS
    'Rolling provider-call budget window in seconds.';
