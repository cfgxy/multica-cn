-- RUYI-425 stage 4: the facts authoritative layer for voice write-back
-- (design doc 2026-10-04-RUYI-423-统一AgentContext架构设计.md §3.3 layers.facts,
-- §3.5 write-back, §3.6 硬原则).
--
-- One row per structured business fact extracted from a live session when the
-- gateway closes it: user decisions, tool calls/results, lifecycle state
-- changes. §3.6 makes this table (with its siblings for other runtimes) the
-- SOLE truth source for what a voice conversation concluded — the transcript
-- and the live_session.summary column are projections that must stay
-- rebuildable from these rows.
--
-- event_id is the idempotency key: the gateway stamps deterministic IDs
-- ("live_session:<id>#<marker>#<kind>"), so a write-back replay after a
-- partial failure re-runs its INSERTs harmlessly. The unique index
-- (workspace_id, event_id) turns "at-least-once delivery + dedup on write"
-- into a database guarantee rather than application discipline.
--
-- seq orders events within one write-back batch; recorded_at carries the
-- observed time. evidence_ref points back at the exact turn that produced
-- the fact (e.g. "live_session:<id>#turn:7").
--
-- agent_id / live_session_id are plain UUIDs on purpose (no FK), same
-- historical-log treatment as live_session (migration 926): facts survive
-- the deletion of the agent or session they refer to, and only cascade with
-- their workspace.
CREATE TABLE agent_fact_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL,
    live_session_id UUID NOT NULL,
    event_id TEXT NOT NULL,
    seq BIGINT NOT NULL DEFAULT 0,
    source_runtime TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('user_decision', 'tool_call', 'tool_result', 'state_change')),
    payload JSONB NOT NULL DEFAULT '{}',
    evidence_ref TEXT NOT NULL DEFAULT '',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, event_id)
);

CREATE INDEX idx_agent_fact_event_agent_seq
    ON agent_fact_event(workspace_id, agent_id, seq);
CREATE INDEX idx_agent_fact_event_session
    ON agent_fact_event(live_session_id, seq);

-- Summary projection for the voice continuity chain (§3.3
-- continuity.voice.last_voice_summary): a template-assembled digest of the
-- facts above, stored next to the session for brief assembly. Rebuildable —
-- never authoritative (§3.6-3); empty until the gateway's write-back lands.
ALTER TABLE live_session ADD COLUMN summary TEXT NOT NULL DEFAULT '';
