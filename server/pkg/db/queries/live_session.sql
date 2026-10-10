-- Live Session persistence (RUYI-425 stage 3, design §3.5). The voice
-- gateway writes one row per websocket session: create on gate pass,
-- session_handle when the provider issues a resumption handle, transcript
-- incrementally as transcription events arrive, and a terminal update when
-- the relay finishes.

-- name: CreateLiveSession :one
-- mode (RUYI-626) tags the transport that opened the session: the gateway
-- relay writes 'gateway', the direct-connect handoff writes 'direct'. The
-- CHECK in migration 937 pins the enum.
INSERT INTO live_session (
    workspace_id, agent_id, runtime_instance_id, user_id, model, context_snapshot, mode
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetLiveSession :one
SELECT * FROM live_session WHERE id = $1;

-- name: SetLiveSessionHandle :exec
-- The provider issues the resumption handle in its sessionResumptionUpdate
-- frame after setup completes; an empty handle (provider never sent one) is
-- a no-op caller-side, never written.
UPDATE live_session SET session_handle = $2, updated_at = now()
WHERE id = $1;

-- name: SetLiveSessionTranscript :exec
-- Incremental flush: the gateway owns the in-memory transcript array and
-- rewrites the whole column per flush (text events are low-rate; audio never
-- touches this column).
UPDATE live_session SET transcript = $2, updated_at = now()
WHERE id = $1;

-- name: EndLiveSession :one
-- Terminal transition, idempotent by caller discipline (the relay ends the
-- session exactly once). Carries the final transcript so an abnormal
-- disconnect still persists everything received so far.
UPDATE live_session
SET status = 'ended', ended_at = now(), transcript = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetLiveSessionSummary :exec
-- RUYI-425 stage 4: write-back projection (design §3.6-3). The summary is a
-- template rebuild of the agent_fact_event rows for this session — never
-- authoritative, always safe to overwrite from facts.
UPDATE live_session SET summary = $2, updated_at = now()
WHERE id = $1;

-- name: ListLatestEndedLiveSessions :many
-- Brief assembly (RUYI-425 stage 4, design §3.5): the most recent closed
-- voice conversation of an agent. LIMIT 1 by the caller's contract, :many so
-- "no voice session yet" is an empty result instead of a row error.
SELECT * FROM live_session
WHERE workspace_id = $1 AND agent_id = $2 AND status = 'ended'
ORDER BY ended_at DESC
LIMIT 1;
