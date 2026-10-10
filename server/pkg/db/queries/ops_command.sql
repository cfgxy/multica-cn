-- RUYI-645: daemon ops command channel + node service inventory.
-- Business rules (state machine, gates, idempotency window, quotas) live in
-- server/internal/service/opscommand.go; these statements are its storage
-- primitives only.

-- name: InsertOpsCommand :one
INSERT INTO daemon_ops_command (
    id, workspace_id, runtime_id, daemon_id, kind, args,
    status, timeout_s, submitted_by_type, submitted_by_id, submission_key
) VALUES (
    $1, $2, $3, $4, $5, $6,
    'pending', $7, $8, $9, $10
)
RETURNING *;

-- name: GetOpsCommand :one
SELECT * FROM daemon_ops_command WHERE id = $1;

-- name: ListOpsCommandsByRuntime :many
-- Command history panel, newest first.
SELECT * FROM daemon_ops_command
WHERE runtime_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: CountInFlightOpsCommands :one
-- Submission quota (design §4): pending + claimed per runtime.
SELECT count(*) FROM daemon_ops_command
WHERE runtime_id = $1 AND status IN ('pending', 'claimed');

-- name: FindOpsCommandBySubmissionKey :many
-- Idempotency probe. :many because a stale row with the same key can exist
-- after the 10-minute window NULLs it (the partial unique index only covers
-- non-NULL keys); the service layer picks the live match.
SELECT * FROM daemon_ops_command
WHERE runtime_id = $1 AND submission_key = $2
ORDER BY created_at DESC;

-- name: ArchiveSubmissionKey :exec
-- Idempotency window expiry: archive the old row's key (equivalent of
-- MeshConsole's archived_key) so the partial unique index frees the key for
-- a fresh submission. Only a non-terminal row keeps its key — terminal rows
-- never block a retry with the same key.
UPDATE daemon_ops_command
SET submission_key = NULL, updated_at = now()
WHERE runtime_id = $1 AND submission_key = $2 AND status IN ('succeeded', 'failed', 'rejected', 'timeout', 'expired');

-- name: ClaimPendingOpsCommands :many
-- Atomic claim (design §1): pending → claimed with the lease epoch bumped,
-- bounded by $2 per heartbeat, in created_at order. The sub-select takes row
-- locks with SKIP LOCKED so two server instances claiming the same runtime
-- in parallel cannot hand out the same command (no double-claim) and cannot
-- deadlock (each claimant skips locked rows instead of waiting).
UPDATE daemon_ops_command
SET status = 'claimed', lease_epoch = lease_epoch + 1, claimed_at = now(), updated_at = now()
WHERE id IN (
    SELECT candidate.id FROM daemon_ops_command AS candidate
    WHERE candidate.runtime_id = $1 AND candidate.status = 'pending'
    ORDER BY candidate.created_at, candidate.id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
RETURNING id, kind, args, timeout_s, lease_epoch;

-- name: FinalizeOpsCommandReceipt :many
-- Daemon receipt: finalize a claimed row only when the echoed lease epoch
-- matches the current generation. Zero rows = stale/mismatched receipt
-- (wrong epoch, already-finalized row, or a claim the server never made).
UPDATE daemon_ops_command
SET status = $3,
    result = $4,
    exit_code = $5,
    reject_reason = $6,
    finished_at = now(),
    updated_at = now()
WHERE id = $1
  AND runtime_id = $2
  AND status = 'claimed'
  AND lease_epoch = $7
RETURNING workspace_id, kind, status, submitted_by_type, submitted_by_id, lease_epoch;

-- name: ExpireStalePendingOpsCommands :many
-- Sweep: pending rows older than the pending TTL were never claimed (the
-- node was offline). They expire and are never re-dispatched (assertion 5).
UPDATE daemon_ops_command
SET status = 'expired', finished_at = now(), updated_at = now(),
    reject_reason = 'pending_ttl_exceeded'
WHERE id IN (
    SELECT id FROM daemon_ops_command
    WHERE status = 'pending' AND created_at < now() - make_interval(secs => sqlc.arg('ttl_seconds')::int)
    LIMIT sqlc.arg('batch_size')::int
    FOR UPDATE SKIP LOCKED
)
RETURNING id, workspace_id, runtime_id, kind;

-- name: TimeoutStaleClaimedOpsCommands :many
-- Sweep: claimed rows whose lease lapsed without a receipt. The lease floor
-- is $1 (the channel default), but a command may legitimately run up to its
-- own timeout_s (up to 300s), so the effective per-command lease is
-- GREATEST(timeout_s, floor) + a 30s receipt-delivery grace — otherwise a
-- long-running whitelisted command would be finalized timeout while the node
-- is still executing it, and its receipt could never land (assertion 3).
UPDATE daemon_ops_command
SET status = 'timeout', finished_at = now(), updated_at = now(),
    reject_reason = 'lease_expired'
WHERE id IN (
    SELECT id FROM daemon_ops_command
    WHERE status = 'claimed'
      AND claimed_at < now() - make_interval(secs => GREATEST(timeout_s, sqlc.arg('lease_seconds')::int) + 30)
    LIMIT sqlc.arg('batch_size')::int
    FOR UPDATE SKIP LOCKED
)
RETURNING id, workspace_id, runtime_id, kind, lease_epoch;

-- name: DeleteWorkspaceOpsCommands :exec
-- Workspace teardown (application-layer graph; no FK by design).
DELETE FROM daemon_ops_command WHERE workspace_id = $1;

-- name: UpsertDaemonServiceInventory :one
INSERT INTO daemon_service_inventory (
    daemon_id, collect_status, services, error, last_report_at, last_success_at, updated_at
) VALUES (
    $1, $2, $3, $4, now(),
    CASE WHEN $2 = 'ok' THEN now() ELSE NULL END,
    now()
)
ON CONFLICT (daemon_id) DO UPDATE SET
    collect_status = EXCLUDED.collect_status,
    services = EXCLUDED.services,
    error = EXCLUDED.error,
    last_report_at = now(),
    last_success_at = CASE WHEN EXCLUDED.collect_status = 'ok' THEN now() ELSE daemon_service_inventory.last_success_at END,
    updated_at = now()
RETURNING *;

-- name: GetDaemonServiceInventory :one
SELECT * FROM daemon_service_inventory WHERE daemon_id = $1;

-- name: ListDaemonInventoryWorkspaces :many
-- Authorization for the daemon-level inventory POST: the reporting daemon
-- must already own runtimes in the caller's workspace. A daemon can only
-- ever register into workspaces its token can prove, so this list is the
-- proof set.
SELECT DISTINCT workspace_id FROM agent_runtime WHERE daemon_id = $1;
