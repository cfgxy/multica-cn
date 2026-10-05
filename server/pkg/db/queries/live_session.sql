-- Live Session persistence (RUYI-425 stage 3, design §3.5). The voice
-- gateway writes one row per websocket session: create on gate pass,
-- session_handle when the provider issues a resumption handle, transcript
-- incrementally as transcription events arrive, and a terminal update when
-- the relay finishes.

-- name: CreateLiveSession :one
INSERT INTO live_session (
    workspace_id, agent_id, runtime_instance_id, user_id, model, context_snapshot
) VALUES ($1, $2, $3, $4, $5, $6)
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
