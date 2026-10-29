ALTER TABLE agent_runtime
    DROP COLUMN IF EXISTS model_call_budget_window_seconds,
    DROP COLUMN IF EXISTS model_call_budget_max_calls,
    DROP COLUMN IF EXISTS model_call_budget_enabled;
