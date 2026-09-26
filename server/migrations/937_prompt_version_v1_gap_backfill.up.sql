-- v1 baseline gap backfill (RUYI-213).
--
-- Migration 922 was a one-time snapshot: it gave every workspace/project/
-- squad/agent that existed at the time a v1 prompt_version row. Nothing wrote
-- a baseline for entities created afterwards, so every one of them has empty
-- version history — "view history" and "diff" show nothing, and the content
-- the entity launched with is unrecoverable. The create paths now seed v1
-- themselves; this file closes the window between 922 and that fix.
--
-- Idempotent by the same guard 922 used: WHERE NOT EXISTS on
-- (scope, scope_id, version = 1). An entity that already has a v1 row — from
-- 922, from the create path, or from a previous run of this file — is left
-- exactly as it is. No existing row is ever updated or deleted, so a project
-- whose instructions changed since creation keeps the older baseline it
-- already had rather than having live content written over its history.
INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT w.id, 'workspace', w.id, 1, COALESCE(w.context, ''), encode(digest(COALESCE(w.context, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM workspace w
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'workspace' AND pv.scope_id = w.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT p.workspace_id, 'project', p.id, 1, COALESCE(p.instructions, ''), encode(digest(COALESCE(p.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM project p
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'project' AND pv.scope_id = p.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT s.workspace_id, 'squad', s.id, 1, COALESCE(s.instructions, ''), encode(digest(COALESCE(s.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM squad s
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'squad' AND pv.scope_id = s.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT a.workspace_id, 'agent', a.id, 1, COALESCE(a.instructions, ''), encode(digest(COALESCE(a.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：补齐缺失的创建时基线'
FROM agent a
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'agent' AND pv.scope_id = a.id AND pv.version = 1
);
