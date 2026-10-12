-- name: GetWorkspaceBackpressureSettings :one
-- Optional row: sql.ErrNoRows means the workspace never saved the card and
-- the caller falls back to the daemon-side code defaults.
SELECT * FROM workspace_backpressure_settings
WHERE workspace_id = $1;

-- name: UpsertWorkspaceBackpressureSettings :one
-- Full-row upsert: the settings card submits every field, so there is no
-- partial-write shape to defend against. updated_at rides the clock.
INSERT INTO workspace_backpressure_settings (
    workspace_id, enabled, mem_high_pct, mem_recovery_pct,
    swap_high_pct, swap_recovery_pct, psi_high_pct, psi_recovery_pct,
    sample_interval_seconds, window_size, updated_by, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now()
)
ON CONFLICT (workspace_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    mem_high_pct = EXCLUDED.mem_high_pct,
    mem_recovery_pct = EXCLUDED.mem_recovery_pct,
    swap_high_pct = EXCLUDED.swap_high_pct,
    swap_recovery_pct = EXCLUDED.swap_recovery_pct,
    psi_high_pct = EXCLUDED.psi_high_pct,
    psi_recovery_pct = EXCLUDED.psi_recovery_pct,
    sample_interval_seconds = EXCLUDED.sample_interval_seconds,
    window_size = EXCLUDED.window_size,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;
