-- One-time v1 baseline backfill (RUYI-183, self-evolution phase 1).
--
-- Every existing workspace/project/squad/agent row already carries live
-- prompt content in its business column, predating prompt_version. Without
-- a v1 row, "view history" and "diff between two versions" would have
-- nothing to show for content that already exists, and the next edit would
-- have to special-case "no prior version" instead of reverting to v1 like
-- any other version.
--
-- source='import' distinguishes this synthetic baseline from a real edit.
-- scanner_revision/gate_result stay at their table defaults ('' / '[]'):
-- this backfill is a mechanical snapshot of already-live content, not a new
-- write path, so it does not run the credential/structure gate that guards
-- actual save/switch/rollback requests.
--
-- Idempotent: a migration record that ran once and left rows behind must
-- not insert a duplicate v1 if this file is ever re-applied against a
-- database that already has them, so every INSERT is guarded by
-- WHERE NOT EXISTS on (scope, scope_id, version = 1).
INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT w.id, 'workspace', w.id, 1, COALESCE(w.context, ''), encode(digest(COALESCE(w.context, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM workspace w
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'workspace' AND pv.scope_id = w.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT p.workspace_id, 'project', p.id, 1, COALESCE(p.instructions, ''), encode(digest(COALESCE(p.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM project p
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'project' AND pv.scope_id = p.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT s.workspace_id, 'squad', s.id, 1, COALESCE(s.instructions, ''), encode(digest(COALESCE(s.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM squad s
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'squad' AND pv.scope_id = s.id AND pv.version = 1
);

INSERT INTO prompt_version (workspace_id, scope, scope_id, version, content, content_sha256, source, change_note)
SELECT a.workspace_id, 'agent', a.id, 1, COALESCE(a.instructions, ''), encode(digest(COALESCE(a.instructions, ''), 'sha256'), 'hex'), 'import', 'v1 基线：自进化上线前既有内容导入'
FROM agent a
WHERE NOT EXISTS (
    SELECT 1 FROM prompt_version pv WHERE pv.scope = 'agent' AND pv.scope_id = a.id AND pv.version = 1
);
