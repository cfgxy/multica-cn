-- RUYI-618 Phase 1: workspace-level host-backpressure settings.
--
-- The nine MULTICA_DAEMON_BACKPRESSURE_* environment knobs are retired: the
-- hysteresis thresholds, sample cadence and smoothing window that gate new
-- task claims under host-memory pressure now live one row per workspace and
-- reach every daemon of that workspace over the heartbeat ack channel. The
-- workspace's settings card is the single authority — systemd units no
-- longer inject any of these values, so a host can never drift from the
-- workspace configuration.
--
-- No row means "never saved": the server then delivers the daemon-side code
-- defaults (server/internal/daemon/config.go Default* constants), so the
-- source of truth stays singular while unconfigured workspaces keep today's
-- behavior.
--
-- workspace_id is a plain UUID PRIMARY KEY on purpose (no FK), same
-- treatment as self_evolution_model_config (migration 934): the row is
-- swept explicitly with the workspace (DeleteWorkspaceLeafData).
CREATE TABLE workspace_backpressure_settings (
    workspace_id UUID PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT true,
    mem_high_pct DOUBLE PRECISION NOT NULL,
    mem_recovery_pct DOUBLE PRECISION NOT NULL,
    swap_high_pct DOUBLE PRECISION NOT NULL,
    swap_recovery_pct DOUBLE PRECISION NOT NULL,
    psi_high_pct DOUBLE PRECISION NOT NULL,
    psi_recovery_pct DOUBLE PRECISION NOT NULL,
    sample_interval_seconds INTEGER NOT NULL,
    window_size INTEGER NOT NULL,
    -- Member who last saved the row (plain UUID, no FK): honest attribution
    -- for scheduling-affecting config, same convention as 935.
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE workspace_backpressure_settings IS
    'Workspace-level host-backpressure gate settings (RUYI-618): the single authority for the watermarks that pause new task claims on daemon hosts; delivered to daemons over heartbeat acks. No row = daemon code defaults.';
COMMENT ON COLUMN workspace_backpressure_settings.enabled IS
    'Gate master switch: false pauses nothing regardless of the watermarks.';
COMMENT ON COLUMN workspace_backpressure_settings.mem_high_pct IS
    'MemAvailable%% below which new claims pause, percent of total (0,100].';
COMMENT ON COLUMN workspace_backpressure_settings.mem_recovery_pct IS
    'MemAvailable%% above which claiming resumes; must exceed mem_high_pct (hysteresis).';
COMMENT ON COLUMN workspace_backpressure_settings.swap_high_pct IS
    'SwapUsed%% above which new claims pause; <=0 disables the swap condition.';
COMMENT ON COLUMN workspace_backpressure_settings.swap_recovery_pct IS
    'SwapUsed%% below which claiming resumes; must sit below swap_high_pct when enabled.';
COMMENT ON COLUMN workspace_backpressure_settings.psi_high_pct IS
    'Memory PSI some-avg10%% above which new claims pause; <=0 disables the PSI condition.';
COMMENT ON COLUMN workspace_backpressure_settings.psi_recovery_pct IS
    'PSI some-avg10%% below which claiming resumes; must sit below psi_high_pct when enabled.';
COMMENT ON COLUMN workspace_backpressure_settings.sample_interval_seconds IS
    'Proc sampling cadence for the watermark gate, in seconds (>=1).';
COMMENT ON COLUMN workspace_backpressure_settings.window_size IS
    'Smoothing window in samples before thresholds are evaluated on the mean (>=1).';
COMMENT ON COLUMN workspace_backpressure_settings.updated_by IS
    'Member who last saved this row; plain UUID on purpose (rows survive member deletion).';
