-- One install row per (workspace, series). Reinstalling the same version is
-- idempotent and updating to a newer version moves this row's pointer, so a
-- second row for the same asset would fragment "installed" and "update
-- available" state.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workspace_prompt_install_workspace_series
    ON workspace_prompt_install (workspace_id, series_id);
