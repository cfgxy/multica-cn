-- RUYI-435: workspace-level quick replies for the issue comment composer.
--
-- One row per configurable reply template. Scope is the workspace: space
-- admins manage the catalog (Web settings tab, MCP tools) and every member's
-- composer menu reads the same rows. Nothing is hardcoded client-side — the
-- 5 seeded templates ship from server/internal/quickreply (same pattern as
-- the issue status catalog): created with the workspace, self-healed on
-- first read for workspaces that predate this table.
--
-- No foreign keys, per repo rule: workspace_id references workspace.id in
-- the application layer only; DeleteWorkspaceAdministration removes the rows
-- during workspace teardown.
--
-- (workspace_id, name) UNIQUE keeps the picker menu unambiguous and mirrors
-- the label catalog's 409-on-duplicate behaviour. The (workspace_id, position)
-- index serves the ordered list read; both indexes are inlined in this batch
-- rather than built CONCURRENTLY — the table is brand new inside this one
-- implicit transaction, so no concurrent reader can observe an unconstrained
-- build window (same rationale as 925).

CREATE TABLE IF NOT EXISTS quick_reply (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    position DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE quick_reply IS
    'Workspace-level quick reply templates for the issue comment composer (RUYI-435). Managed by workspace owner/admin via the Web settings tab or the MCP quick-reply tools; read by every member. Selecting one fills the composer without sending. Seeded per workspace by server/internal/quickreply.Ensure.';
COMMENT ON COLUMN quick_reply.position IS
    'Display order, ascending. Fractional values let a new entry slot between neighbours without rewriting them.';

CREATE UNIQUE INDEX IF NOT EXISTS idx_quick_reply_workspace_name
    ON quick_reply (workspace_id, name);

CREATE INDEX IF NOT EXISTS idx_quick_reply_workspace_position
    ON quick_reply (workspace_id, position);
