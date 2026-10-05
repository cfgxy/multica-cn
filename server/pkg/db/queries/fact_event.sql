-- Agent fact events (RUYI-425 stage 4, design §3.3 layers.facts / §3.6): the
-- authoritative, replayable record of business facts extracted from a voice
-- session. Idempotency is a database guarantee — the unique
-- (workspace_id, event_id) index makes a write-back replay a no-op.

-- name: InsertFactEvent :execrows
-- ON CONFLICT DO NOTHING is the write-side half of at-least-once delivery:
-- the gateway may retry after a transient failure, and a replayed batch
-- collapses to zero duplicate rows.
INSERT INTO agent_fact_event (
    workspace_id, agent_id, live_session_id, event_id, seq,
    source_runtime, kind, payload, evidence_ref, recorded_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, event_id) DO NOTHING;

-- name: ListFactEventsBySession :many
-- The full ordered fact list of one voice session — the rebuild input for
-- the summary projection and for audit reads.
SELECT * FROM agent_fact_event
WHERE workspace_id = $1 AND live_session_id = $2
ORDER BY seq, recorded_at;

-- name: ListRecentVoiceFactsForBrief :many
-- PriorContextBrief voice section (design §3.5): facts of the agent's most
-- recent closed live session, newest last. Capped so the brief block stays
-- inside its token budget; the summary projection carries the whole-session
-- context, these rows carry the individually citable facts.
SELECT e.* FROM agent_fact_event e
JOIN live_session s ON s.id = e.live_session_id
WHERE e.workspace_id = $1 AND e.agent_id = $2 AND s.status = 'ended'
ORDER BY s.ended_at DESC, e.seq DESC
LIMIT $3;
