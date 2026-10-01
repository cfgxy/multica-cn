-- Runtime skill discovery index: a workspace-scoped mirror of the skills each
-- connected runtime's local discovery surfaced (see
-- server/internal/daemon/local_skills.go). Rows are metadata-only summaries —
-- the authoritative skill bodies stay in `skill`, written by the existing
-- import flow when a discovered skill is actually brought into the workspace.
-- No foreign keys by repo rule; workspace teardown deletes these rows
-- explicitly in DeleteWorkspaceSquadsAndSkills.
CREATE TABLE runtime_skill_discovery (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    runtime_id TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    root TEXT NOT NULL DEFAULT '',
    plugin TEXT NOT NULL DEFAULT '',
    key TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    source_path TEXT NOT NULL DEFAULT '',
    file_count INT NOT NULL DEFAULT 1,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
