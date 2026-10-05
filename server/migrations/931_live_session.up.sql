-- RUYI-425 stage 3: Live Session table (design doc
-- 2026-10-04-RUYI-423-统一AgentContext架构设计.md §3.5 / §6 新增).
-- One row per voice conversation the gateway opens against a Gemini Live
-- instance. Lifecycle: the gateway inserts with the default status 'active'
-- when the client's websocket session passes the initiation gate
-- (VOICE_UNAVAILABLE checks, §4.4 rule 3), then flips to 'ended' with
-- ended_at when the relay finishes — normally or mid-stream.
--
-- agent_id / runtime_instance_id / user_id are plain UUIDs on purpose: this
-- is a historical log table, so rows must survive the deletion of the agent,
-- instance or member they once pointed at (the same treatment migration 004
-- gave agent_runtime rows, which carry no FKs into agent either). Only the
-- workspace link is a real FK — cascade on workspace teardown, the house
-- pattern for workspace-scoped tables.
--
-- context_snapshot freezes the effective configuration at session start
-- (§4.5 配置变更版本一致性): model, instructions hash, agent context digest —
-- an audit can answer "which context version did this conversation run on"
-- even after the agent's config changed mid-flight.
--
-- transcript is the persisted two-way transcription (§3.5 转录落库): a JSON
-- array of {role, text, at} events, flushed incrementally by the gateway and
-- finalized on session end. It is a projection layer record, never the only
-- carrier of business facts (§3.6) — the facts layer is stage 4's scope.
CREATE TABLE live_session (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL,
    runtime_instance_id UUID NOT NULL,
    user_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'ended')),
    model TEXT NOT NULL DEFAULT '',
    context_snapshot JSONB NOT NULL DEFAULT '{}',
    session_handle TEXT NOT NULL DEFAULT '',
    transcript JSONB NOT NULL DEFAULT '[]',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_live_session_agent_started
    ON live_session(workspace_id, agent_id, started_at DESC);
CREATE INDEX idx_live_session_runtime_started
    ON live_session(runtime_instance_id, started_at DESC);
