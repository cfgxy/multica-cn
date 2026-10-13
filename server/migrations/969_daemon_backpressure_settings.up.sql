-- RUYI-729 stage 1: daemon-level host-backpressure settings + trigger
-- event history.
--
-- daemon_backpressure_settings is one row per daemon host (agent_runtime.
-- daemon_id TEXT domain): the Runtime detail page reads and saves it, the
-- heartbeat ack delivers it, and the daemon hot-rebuilds its hysteresis
-- gate from it without a restart. No row means "never saved": the server
-- then delivers the daemon code defaults (server/pkg/protocol/backpressure
-- Default* constants) so the source of truth stays singular while
-- unconfigured hosts keep today's env-driven behavior. The env knobs stay
-- loaded at daemon boot as the fallback defaults and are overridden by the
-- first saved row.
--
-- workspace_id anchors the row to the workspace whose Runtime page saved it
-- (plain UUID, no FK per repo rule). It is the audit/sweep anchor, not an
-- authorization boundary — the write path re-resolves the workspace from
-- the runtime row. A daemon serves exactly one workspace's runtimes in this
-- fork's deployment shape; if a daemon_id is ever re-registered under a
-- different workspace, the save path overwrites the row wholesale, so no
-- stale cross-workspace row can survive.
--
-- daemon_backpressure_event is the trigger/recovery history (④): the server
-- appends one row per observed active/cleared transition of a runtime's
-- backpressure report, carrying the hit detail (condition, value,
-- threshold) and the effective config snapshot so a "why did everything
-- stall at noon" question has a queryable answer. Volume is two rows per
-- episode per runtime — small enough to keep, no TTL.
--
-- The event-table index is inlined rather than built CONCURRENTLY: the
-- table is brand new inside this one implicit transaction, so no concurrent
-- reader can observe an unconstrained build window (same rationale as
-- 939/929). No foreign keys anywhere, per repo rule; rows are swept
-- explicitly with the workspace (DeleteWorkspaceLeafData).

CREATE TABLE daemon_backpressure_settings (
    daemon_id TEXT PRIMARY KEY,
    workspace_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    mem_high_pct DOUBLE PRECISION NOT NULL,
    mem_recovery_pct DOUBLE PRECISION NOT NULL,
    swap_high_pct DOUBLE PRECISION NOT NULL,
    swap_recovery_pct DOUBLE PRECISION NOT NULL,
    psi_high_pct DOUBLE PRECISION NOT NULL,
    psi_recovery_pct DOUBLE PRECISION NOT NULL,
    sample_interval_seconds INTEGER NOT NULL,
    window_size INTEGER NOT NULL,
    -- Bumped on every save; the daemon compares versions across heartbeat
    -- acks so an unchanged config is a pointer no-op on the daemon side.
    config_version BIGINT NOT NULL DEFAULT 1,
    -- Member who last saved the row (plain UUID, no FK): honest attribution
    -- for scheduling-affecting config, same convention as 935/939.
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE daemon_backpressure_settings IS
    'Daemon-level host-backpressure gate settings (RUYI-729): the single authority for the watermarks that pause new task claims on a daemon host; delivered to the daemon over heartbeat acks. No row = daemon code defaults.';
COMMENT ON COLUMN daemon_backpressure_settings.daemon_id IS
    'The daemon host this config gates; same TEXT identity as agent_runtime.daemon_id.';
COMMENT ON COLUMN daemon_backpressure_settings.enabled IS
    'Gate master switch: false pauses nothing regardless of the watermarks.';
COMMENT ON COLUMN daemon_backpressure_settings.mem_high_pct IS
    'MemAvailable%% below which new claims pause, percent of total (0,100].';
COMMENT ON COLUMN daemon_backpressure_settings.mem_recovery_pct IS
    'MemAvailable%% above which claiming resumes; must exceed mem_high_pct (hysteresis).';
COMMENT ON COLUMN daemon_backpressure_settings.swap_high_pct IS
    'SwapUsed%% above which new claims pause; <=0 disables the swap condition.';
COMMENT ON COLUMN daemon_backpressure_settings.swap_recovery_pct IS
    'SwapUsed%% below which claiming resumes; must sit below swap_high_pct when enabled.';
COMMENT ON COLUMN daemon_backpressure_settings.psi_high_pct IS
    'Memory PSI some-avg10%% above which new claims pause; <=0 disables the PSI condition.';
COMMENT ON COLUMN daemon_backpressure_settings.psi_recovery_pct IS
    'PSI some-avg10%% below which claiming resumes; must sit below psi_high_pct when enabled.';
COMMENT ON COLUMN daemon_backpressure_settings.sample_interval_seconds IS
    'Proc sampling cadence for the watermark gate, in seconds (>=1).';
COMMENT ON COLUMN daemon_backpressure_settings.window_size IS
    'Smoothing window in samples before thresholds are evaluated on the mean (>=1).';
COMMENT ON COLUMN daemon_backpressure_settings.config_version IS
    'Incremented on every save; daemons use it to detect a changed card.';
COMMENT ON COLUMN daemon_backpressure_settings.updated_by IS
    'Member who last saved this row; plain UUID on purpose (rows survive member deletion).';

CREATE TABLE daemon_backpressure_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    runtime_id UUID NOT NULL,
    daemon_id TEXT NOT NULL,
    workspace_id UUID NOT NULL,
    -- 'entered' (claims paused) or 'cleared' (claims resumed); the
    -- transition vocabulary mirrors the daemon/server log lines.
    event_type TEXT NOT NULL,
    -- "+"-joined triggered conditions at enter time ("mem+swap"); empty on
    -- cleared events.
    reason TEXT NOT NULL DEFAULT '',
    -- Smoothed watermarks at event time, mirroring the report fields.
    mem_available_pct DOUBLE PRECISION NOT NULL,
    swap_used_pct DOUBLE PRECISION NOT NULL,
    psi_some_avg10 DOUBLE PRECISION NOT NULL,
    psi_read_ok BOOLEAN NOT NULL DEFAULT false,
    -- Claim polls the daemon skipped during the episode, stamped on the
    -- cleared event.
    deferred_claims BIGINT NOT NULL DEFAULT 0,
    -- The effective gate config at event time (JSON snapshot of the wire
    -- card), so history stays interpretable after the settings change.
    effective_config JSONB NOT NULL,
    -- [{condition,value,threshold}] for every triggered condition at enter
    -- time; empty on cleared events.
    hits JSONB NOT NULL DEFAULT '[]',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE daemon_backpressure_event IS
    'Backpressure trigger/recovery history (RUYI-729): one row per observed active->inactive transition of a runtime''s host-backpressure report, with hit detail and the effective config snapshot.';

CREATE INDEX idx_daemon_backpressure_event_runtime
    ON daemon_backpressure_event (runtime_id, occurred_at DESC);
