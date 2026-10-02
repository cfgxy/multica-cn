-- RUYI-351 structured issue relations on the legacy issue_dependency edge
-- table (001_init). issue_id is the edge source, depends_on_issue_id the
-- edge target. Edges store one canonical row per relation: 'blocks' and
-- 'supersedes' keep the forward row only ('blocked_by' / 'superseded_by'
-- are query-side inverses resolved by the handler), and the symmetric
-- 'relates_to' stores one row per unordered pair with the lexicographically
-- smaller UUID first. The table's 001-era FK cascade removes edges whenever
-- either endpoint issue is deleted, so no delete path can leave a dangling
-- relation behind.

-- ON CONFLICT DO NOTHING + RETURNING: no returned row means the exact edge
-- already exists (uq_issue_dependency_edge), which the handler surfaces as
-- 409 relation_exists.
-- name: InsertIssueRelation :one
INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING
RETURNING id, issue_id, depends_on_issue_id, type, created_at;

-- name: DeleteIssueRelation :one
DELETE FROM issue_dependency
WHERE issue_id = $1 AND depends_on_issue_id = $2 AND type = $3
RETURNING id;

-- name: ListIssueRelationsForIssue :many
SELECT d.id, d.type, d.issue_id, d.depends_on_issue_id, d.created_at,
       i.id AS other_id, i.number AS other_number, i.title AS other_title,
       i.status AS other_status
FROM issue_dependency d
JOIN issue i
  ON i.id = CASE WHEN d.issue_id = $1 THEN d.depends_on_issue_id ELSE d.issue_id END
WHERE d.issue_id = $1 OR d.depends_on_issue_id = $1
ORDER BY d.created_at, i.id;

-- Optimistic-lock bump for the anchor issue of a relation write: returns no
-- row when expected_revision is stale, which the handler surfaces as
-- 409 revision_conflict (the caller rolls the edge change back with it).
-- name: BumpIssueRevisionGuarded :one
UPDATE issue
SET revision = revision + 1, updated_at = now(), last_activity_at = now()
WHERE id = $1
  AND (sqlc.narg('expected_revision')::bigint IS NULL OR revision = sqlc.narg('expected_revision')::bigint)
RETURNING id, revision;

-- Counterpart bump for the other endpoint: a committed relation is part of
-- both issues' observable state, so both revisions advance. Callers bump the
-- two endpoints in canonical UUID order to keep row-lock order consistent
-- across concurrent relation writes.
-- name: BumpIssueRevision :exec
UPDATE issue
SET revision = revision + 1, updated_at = now(), last_activity_at = now()
WHERE id = $1;
