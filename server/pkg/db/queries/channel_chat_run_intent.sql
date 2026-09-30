-- =====================
-- channel_chat_run_intent
-- =====================
-- RUYI-304: durable run-trigger intent ledger for channel ordinary messages.
-- The append transaction upserts one 'pending' row per (chat_session,
-- context_revision); the debounced flush and the compensating reconciler both
-- clear it inside their enqueue transaction. No column ever carries message
-- bodies or credentials — trigger identity only.

-- name: UpsertChatRunIntent :exec
-- Window collapse: while the row is 'pending' every later message in the same
-- debounce window extends the SAME row (fire_at pushed out, latest sender
-- wins, per MUL-2645). The conflict target is the partial unique index
-- (968); a settled row ('fired'/'dead') no longer conflicts, so the next
-- window on the same generation inserts a fresh pending row.
INSERT INTO channel_chat_run_intent (
    id, workspace_id, chat_session_id, context_revision,
    initiator_user_id, force_fresh, binding_id, route_revision, installation_id,
    state, fire_at, created_at, updated_at
) VALUES (
    @id, @workspace_id, @chat_session_id, @context_revision,
    @initiator_user_id, @force_fresh, @binding_id, @route_revision, @installation_id,
    'pending', now() + @fire_delay::interval, now(), now()
)
ON CONFLICT (chat_session_id, context_revision) WHERE state = 'pending' DO UPDATE SET
    initiator_user_id = EXCLUDED.initiator_user_id,
    force_fresh       = EXCLUDED.force_fresh,
    binding_id        = EXCLUDED.binding_id,
    route_revision    = EXCLUDED.route_revision,
    installation_id   = EXCLUDED.installation_id,
    fire_at           = now() + @fire_delay::interval,
    updated_at        = now();

-- name: FireChatRunIntents :many
-- The idempotency anchor: called inside the enqueue transaction AFTER the
-- session/agent/delivery fences pass, BEFORE the task row is created. Empty
-- result means either another trigger already fired this generation (check
-- GetSettledChatRunIntentState to tell that apart from "no ledger row") or no
-- intent row exists (first-party EnqueueChatTask, /new, or a race) — in both
-- "no row" cases the caller proceeds as before. A later failure in the same
-- transaction rolls the flip back, so the row stays pending for the
-- reconciler.
UPDATE channel_chat_run_intent
SET state = 'fired',
    claimed_by = NULL,
    claim_expires_at = NULL,
    updated_at = now()
WHERE chat_session_id = @chat_session_id
  AND context_revision = @context_revision
  AND state = 'pending'
RETURNING id;

-- name: GetSettledChatRunIntentState :one
-- Distinguishes "another trigger already fired this generation" (a settled row
-- exists → skip the enqueue) from "no ledger row" (proceed). Returns the most
-- recently updated settled row's state.
SELECT state
FROM channel_chat_run_intent
WHERE chat_session_id = @chat_session_id
  AND context_revision = @context_revision
  AND state != 'pending'
ORDER BY updated_at DESC
LIMIT 1;

-- name: ClaimNextDueChatRunIntent :one
-- Short-transaction claim of ONE due row, taken immediately before that row is
-- reconciled — one claim per row keeps attempt counts honest (same discipline
-- as ClaimNextChannelMediaPendingObjectForReconcile). Due = 'pending', past
-- its fire_at, not leased (or lease expired) and past its retry backoff.
-- FOR UPDATE SKIP LOCKED keeps replicas off each other's row; the enqueue runs
-- outside the claim transaction, gated by the lease token. ErrNoRows = sweep
-- done. AS MATERIALIZED pins the candidate subquery (RUYI-277 family).
UPDATE channel_chat_run_intent AS intent
SET claimed_by = @lease_token,
    claim_expires_at = now() + @lease::interval,
    updated_at = now()
WHERE intent.id = (
    SELECT candidate.id
    FROM (
        SELECT due.id
        FROM channel_chat_run_intent AS due
        WHERE due.state = 'pending'
          AND due.fire_at <= now()
          AND (due.claimed_by IS NULL OR due.claim_expires_at IS NULL OR due.claim_expires_at < now())
          AND (due.next_attempt_at IS NULL OR due.next_attempt_at <= now())
        ORDER BY due.fire_at
        LIMIT 1
        FOR UPDATE OF due SKIP LOCKED
    ) AS candidate
)
RETURNING intent.*;

-- name: ReleaseChatRunIntentForRetry :execrows
-- Lease-guarded transient-failure release: drop the lease, back off the next
-- attempt, keep the row pending. The lease token guard means a row reclaimed
-- after expiry ignores the old owner's write. Attempts counts real tries.
UPDATE channel_chat_run_intent
SET claimed_by = NULL,
    claim_expires_at = NULL,
    attempts = attempts + 1,
    next_attempt_at = now() + @backoff::interval,
    last_error = @last_error,
    updated_at = now()
WHERE id = @id
  AND claimed_by = @lease_token
  AND state = 'pending';

-- name: FailChatRunIntentByRevision :execrows
-- Lease-agnostic pending→dead transition by trigger key; the flush path uses
-- it when its enqueue failed permanently. Winner-of-CAS semantics: exactly one
-- caller flips the row, and that caller owns the user-visible terminal notice.
UPDATE channel_chat_run_intent
SET state = 'dead',
    dead_reason = @dead_reason,
    last_error = @last_error,
    claimed_by = NULL,
    claim_expires_at = NULL,
    updated_at = now()
WHERE chat_session_id = @chat_session_id
  AND context_revision = @context_revision
  AND state = 'pending';

-- name: FailClaimedChatRunIntent :execrows
-- Lease-guarded pending→dead transition for the reconciler: a flush that
-- already terminalized the row (FailChatRunIntentByRevision ignores leases)
-- makes this a no-op, so exactly one path emits the terminal notice.
UPDATE channel_chat_run_intent
SET state = 'dead',
    dead_reason = @dead_reason,
    last_error = @last_error,
    updated_at = now()
WHERE id = @id
  AND claimed_by = @lease_token
  AND state = 'pending';
