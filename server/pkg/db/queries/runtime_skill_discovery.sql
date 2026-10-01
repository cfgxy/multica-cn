-- Runtime skill discovery index (RUYI-288): workspace-scoped mirror of the
-- skill summaries each connected runtime's local discovery reports. Metadata
-- only — the authoritative skill body is written by the regular runtime-local
-- import flow when a discovered skill is brought into the workspace.

-- name: UpsertRuntimeSkillDiscovery :exec
INSERT INTO runtime_skill_discovery (
    workspace_id, runtime_id, provider, root, plugin, key, name, description,
    source_path, file_count
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, runtime_id, key) DO UPDATE SET
    provider = EXCLUDED.provider,
    root = EXCLUDED.root,
    plugin = EXCLUDED.plugin,
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    source_path = EXCLUDED.source_path,
    file_count = EXCLUDED.file_count,
    last_seen_at = now();

-- name: DeleteRuntimeSkillDiscoveriesExcept :exec
-- Prune sightings a fresh discovery no longer reports (the skill was removed
-- from the machine). Scoped to the reporting runtime: another runtime may
-- still legitimately see the same key from a shared root.
DELETE FROM runtime_skill_discovery
WHERE workspace_id = $1
  AND runtime_id = $2
  AND key <> ALL(sqlc.arg('keys')::text[]);

-- name: ListRuntimeSkillDiscoveries :many
SELECT * FROM runtime_skill_discovery
WHERE workspace_id = $1
ORDER BY name ASC;
