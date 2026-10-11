-- RUYI-641 stage A: user-defined parameters for an autopilot. One row per
-- declared parameter (name, type, description, required flag, default value,
-- enum options) in the autopilot's calling contract. Triggers may pass values
-- for these parameters (webhook body `params` object, manual/API /trigger
-- request `params`); schedule fires resolve defaults; unresolved required
-- parameters or type mismatches reject the dispatch with the reason recorded
-- on the run. Resolved values are snapshotted into autopilot_run.trigger_payload
-- for audit replay, and substitute {{name}} tokens in the issue-title template
-- and task instructions.
--
-- The name doubles as a template token, so it is restricted to identifier
-- syntax; "date" is reserved for the built-in {{date}} token (enforced in the
-- service layer, not here, so a future built-in rename does not need a
-- migration).
--
-- autopilot_id is a plain UUID by house pattern: product delete is archival
-- and the only hard delete is workspace teardown, whose explicit deletion
-- graph owns the cleanup (DeleteWorkspaceAutopilotChildren). Only the
-- workspace link is a real FK — cascade on workspace teardown, the same
-- treatment migration 931 gave workspace-scoped tables.
CREATE TABLE autopilot_param (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    autopilot_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]{0,49}$'),
    param_type TEXT NOT NULL CHECK (param_type IN ('string', 'text', 'number', 'boolean', 'enum')),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    required BOOLEAN NOT NULL DEFAULT false,
    default_value TEXT,
    enum_options TEXT[] NOT NULL DEFAULT '{}',
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (autopilot_id, name)
);

CREATE INDEX idx_autopilot_param_autopilot ON autopilot_param(autopilot_id, position);
