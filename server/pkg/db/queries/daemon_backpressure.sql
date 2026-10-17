-- RUYI-729 stage 1: daemon-level host-backpressure settings + trigger event
-- history. The Runtime detail page saves one row per daemon host
-- (agent_runtime.daemon_id domain); the heartbeat ack reads it for delivery;
-- the transition recorder appends events. No FK anywhere (house rule): the
-- workspace sweep deletes these rows explicitly.

-- name: GetDaemonBackpressureSettings :one
SELECT * FROM daemon_backpressure_settings WHERE daemon_id = $1;

-- name: UpsertDaemonBackpressureSettings :one
-- config_version bumps on every write, including the update path, so the
-- daemon can cheaply compare the delivered card against what it applied.
-- updated_at is stamped here rather than by a trigger so the audit trail
-- and the wire card carry one consistent write instant.
INSERT INTO daemon_backpressure_settings (
    daemon_id, workspace_id, enabled,
    mem_high_pct, mem_recovery_pct,
    swap_high_pct, swap_recovery_pct,
    psi_high_pct, psi_recovery_pct,
    sample_interval_seconds, window_size, updated_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (daemon_id) DO UPDATE SET
    workspace_id = EXCLUDED.workspace_id,
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
    config_version = daemon_backpressure_settings.config_version + 1,
    updated_at = now()
RETURNING *;

-- name: DeleteDaemonBackpressureSettings :exec
-- "Restore daemon defaults" deletes the row; the next heartbeat ack then
-- delivers the code defaults again.
DELETE FROM daemon_backpressure_settings WHERE daemon_id = $1;

-- name: InsertDaemonBackpressureEvent :one
INSERT INTO daemon_backpressure_event (
    runtime_id, daemon_id, workspace_id, event_type, reason,
    mem_available_pct, swap_used_pct, psi_some_avg10, psi_read_ok,
    deferred_claims, effective_config, hits
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: ListDaemonBackpressureEvents :many
-- Newest-first timeline for the Runtime detail page; limit is bounded by
-- the handler. Uses idx_daemon_backpressure_event_runtime (migration 969).
SELECT * FROM daemon_backpressure_event
WHERE runtime_id = $1
ORDER BY occurred_at DESC, id DESC
LIMIT $2;
