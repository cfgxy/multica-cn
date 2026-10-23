-- RUYI-859 stage A: agent-level error-rate backpressure policy + suppression
-- event history (config surface and read-only visibility; the automatic
-- suppression loop is stage B and is NOT wired by this migration).
--
-- agent_backpressure_policy anchors on (workspace_id, agent_id): one row per
-- agent whose workspace admin explicitly saved a policy. Plain UUID columns,
-- no foreign keys, per repo rule. A missing row parses as the built-in
-- defaults (read-only monitoring, automatic suppression OFF) in the API
-- layer — the migration never seeds rows, so an unsaved agent keeps the
-- singular source of truth in the handler defaults.
--
-- The two failure-class inclusion switches define which terminal-sample
-- classes count toward the error-rate numerator:
--   - count_model_api_failures   -> the auth/rate_limit/timeout/provider
--     display classes (model/API-side failures),
--   - count_execution_failures   -> the runtime/agent/other classes
--     (platform execution failures, including unclassified).
-- Both default to true. Class membership is computed in Go from the
-- taskfailure display classes; the stored failure_reason stays the source of
-- truth (see server/pkg/taskfailure).
--
-- recovery_window_minutes and the fixed 0.5x recovery threshold (stage B)
-- are stored but only interpreted by the automatic loop; stage A treats
-- them as read-only display data. No row change here can pause anything:
-- the claim gate and scheduling_pause are untouched.
--
-- agent_suppression_event records one row per entered/cleared transition of
-- the automatic error-rate suppression (stage B writes it; stage A only
-- reads and displays it). effective_policy snapshots the policy card in
-- force, mirroring daemon_backpressure_event.effective_config, so a "why
-- was this agent suppressed at noon" question stays answerable after the
-- config changes. Volume is two rows per episode per agent — small enough
-- to keep, no TTL.
--
-- The event table is brand new inside this single implicit transaction, so
-- its index (the "recent events" read on the agent detail page) is built
-- CONCURRENTLY in its own single-statement file: 983 occupies the separate
-- allocation number the repo index rule requires for a concurrent build
-- (reported in the issue thread; the 975/979 multi-file same-number pattern
-- would also satisfy the one-number rule, but a separate stem is the cleanest
-- expression of "one index build, one file, outside any transaction").
-- The policy table carries no explicit index at all: its (workspace_id,
-- agent_id) composite primary key is table DDL, not a created index.
--
-- Rows are swept explicitly with the workspace (DeleteWorkspaceLeafData),
-- same treatment as daemon_backpressure_event (RUYI-729).

CREATE TABLE agent_backpressure_policy (
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    -- One policy row per agent: the composite primary key makes a second
    -- row for the same (workspace, agent) pair impossible at the storage
    -- layer, so the API upsert can ON CONFLICT on the primary key.
    PRIMARY KEY (workspace_id, agent_id),
    -- Error-rate aggregation window for entry judgment, in minutes (>=1).
    window_minutes INTEGER NOT NULL DEFAULT 60,
    -- Aggregated error rate (percent, 0-100] that arms the suppression.
    error_rate_threshold_pct DOUBLE PRECISION NOT NULL DEFAULT 30,
    -- Minimum terminal samples inside the window before the rate counts
    -- (guards a small-sample fluke from arming).
    min_samples INTEGER NOT NULL DEFAULT 5,
    -- Full trailing window the recovery judgment aggregates over, in
    -- minutes (>=1). The recovery threshold is fixed at
    -- error_rate_threshold_pct * 0.5 — not a separate field, so the form
    -- never carries a second, independently editable threshold (RUYI-740
    -- form-usability ruling carried into RUYI-859 stage A).
    recovery_window_minutes INTEGER NOT NULL DEFAULT 30,
    -- Automatic suppression master switch, default OFF: an unsaved or
    -- default agent is monitored only, never auto-paused.
    suppress_on_error_rate BOOLEAN NOT NULL DEFAULT false,
    -- Failure-class inclusion switches; see the table comment.
    count_model_api_failures BOOLEAN NOT NULL DEFAULT true,
    count_execution_failures BOOLEAN NOT NULL DEFAULT true,
    -- Member who last saved the row (plain UUID, no FK, same convention as
    -- 938/969); NULL never occurs — the row is only created by a save.
    updated_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE agent_backpressure_policy IS
    'Agent-level error-rate backpressure policy (RUYI-859 stage A): per-agent aggregation window, threshold, sample floor, recovery window, auto-suppression switch and failure-class inclusion. No row = defaults (monitor only, auto-suppression off).';
COMMENT ON COLUMN agent_backpressure_policy.window_minutes IS
    'Trailing window, in minutes, over which the entry judgment aggregates terminal task samples.';
COMMENT ON COLUMN agent_backpressure_policy.error_rate_threshold_pct IS
    'Percent (0,100] of in-window failed terminal samples that arms the automatic suppression; the recovery threshold is fixed at 0.5x this value.';
COMMENT ON COLUMN agent_backpressure_policy.min_samples IS
    'Minimum in-window terminal samples for the error rate to be evaluated at all.';
COMMENT ON COLUMN agent_backpressure_policy.recovery_window_minutes IS
    'Full trailing window, in minutes, the recovery judgment aggregates over; the judgment must reach min_samples and stay below the fixed 0.5x threshold before the suppression clears.';
COMMENT ON COLUMN agent_backpressure_policy.suppress_on_error_rate IS
    'Automatic suppression master switch; default false means the policy only drives the read-only visibility card.';
COMMENT ON COLUMN agent_backpressure_policy.count_model_api_failures IS
    'Whether model/API-side failure display classes (auth, rate_limit, timeout, provider) count toward the error-rate numerator.';
COMMENT ON COLUMN agent_backpressure_policy.count_execution_failures IS
    'Whether platform execution failure display classes (runtime, agent, other including unclassified) count toward the error-rate numerator.';
COMMENT ON COLUMN agent_backpressure_policy.updated_by IS
    'Member who last saved this row; plain UUID on purpose (rows survive member deletion).';

CREATE TABLE agent_suppression_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    -- 'entered' (auto-suppression armed) or 'cleared' (lifted).
    event_type TEXT NOT NULL,
    -- Terminal samples aggregated inside the judgment window at event time.
    sample_count INTEGER NOT NULL,
    -- Aggregated error rate (percent) at event time.
    error_rate_pct DOUBLE PRECISION NOT NULL,
    -- The effective policy card in force at event time (JSON snapshot of the
    -- API read model), so history stays interpretable after the settings
    -- change — same rationale as daemon_backpressure_event.effective_config.
    effective_policy JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE agent_suppression_event IS
    'Automatic error-rate suppression history (RUYI-859 stage B writer, stage A reader): one row per entered/cleared transition, with sample count, error rate and the effective policy snapshot.';
