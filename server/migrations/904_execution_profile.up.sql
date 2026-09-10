-- Execution profiles (RUYI-57): workspace-level named sets of per-agent
-- execution configuration (runtime + model + thinking level). Activating a
-- profile overwrites the current configuration of every agent the profile
-- names; agents outside the profile are untouched.
--
-- This is a DIFFERENT concept from runtime_profile (MUL-3284), which defines
-- how a custom runtime is launched. An execution_profile stores *which*
-- runtime/model each agent should run on. The UI copy says "Profile" for both;
-- the code, tables and API say execution_profile so the two never collide.
--
-- No foreign keys by house rule: workspace_id / created_by / profile_id /
-- agent_id / runtime_id integrity is enforced in the application layer. The
-- activate path re-validates every entry against the workspace before writing,
-- so a stale agent_id or runtime_id degrades to a per-entry skip, never a
-- partial cross-workspace write.
CREATE TABLE execution_profile (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    description TEXT,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set on every successful activation; drives the "last activated" column
    -- in the manage drawer without a second query against activity_log.
    last_activated_at TIMESTAMPTZ
);

-- One row per (profile, agent). runtime_id and model are NOT NULL because an
-- entry is only meaningful when it can actually be applied: activation writes
-- both columns onto the agent in one update, so a half-configured entry would
-- be an activatable no-op that silently leaves the agent on its old runtime.
-- The API therefore only accepts an entry carrying both values.
--
-- thinking_level is nullable: not every provider exposes reasoning effort, and
-- NULL means "leave the agent's thinking_level alone on activation" (as opposed
-- to '' which the agent API reserves for an explicit clear).
CREATE TABLE execution_profile_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    model TEXT NOT NULL CHECK (char_length(model) BETWEEN 1 AND 200),
    thinking_level TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pointer to the profile activated last. Cleared (not cascaded) when that
-- profile is deleted: deleting a profile never rolls back configuration that
-- has already been written onto the agents.
ALTER TABLE workspace ADD COLUMN IF NOT EXISTS active_execution_profile_id UUID;

COMMENT ON COLUMN workspace.active_execution_profile_id IS
    'Execution profile most recently activated in this workspace (RUYI-57). Display state only: it records which profile last wrote the agents'' runtime/model/thinking_level, not a live binding. Cleared when that profile is deleted; the agents keep the configuration the activation wrote. No FK, app-layer integrity.';
