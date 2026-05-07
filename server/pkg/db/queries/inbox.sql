-- name: ListInboxItemsLatest :many
-- Newest-first inbox listing with per-issue dedup (Linear-style: an issue
-- with multiple unarchived notifications appears once, with the newest
-- entry winning). The DISTINCT ON dedups; the outer slice applies the
-- cursor page LIMIT. COALESCE(issue_id, id) keeps system notifications
-- (issue_id NULL) one-per-row.
SELECT id, workspace_id, recipient_type, recipient_id, type, severity,
       issue_id, title, body, read, archived, created_at, actor_type,
       actor_id, details, issue_status FROM (
  SELECT DISTINCT ON (COALESCE(i.issue_id, i.id)) i.*, iss.status AS issue_status
  FROM inbox_item i
  LEFT JOIN issue iss ON iss.id = i.issue_id
  WHERE i.workspace_id = $1 AND i.recipient_type = $2 AND i.recipient_id = $3 AND i.archived = false
  ORDER BY COALESCE(i.issue_id, i.id), i.created_at DESC, i.id DESC
) AS deduped
ORDER BY created_at DESC, id DESC
LIMIT $4;

-- name: ListInboxItemsBefore :many
-- Cursor-paginated older-than-cursor slice with the same per-issue dedup as
-- ListInboxItemsLatest. The (created_at, id) tuple keyset is exclusive of
-- the cursor row.
SELECT id, workspace_id, recipient_type, recipient_id, type, severity,
       issue_id, title, body, read, archived, created_at, actor_type,
       actor_id, details, issue_status FROM (
  SELECT DISTINCT ON (COALESCE(i.issue_id, i.id)) i.*, iss.status AS issue_status
  FROM inbox_item i
  LEFT JOIN issue iss ON iss.id = i.issue_id
  WHERE i.workspace_id = $1 AND i.recipient_type = $2 AND i.recipient_id = $3 AND i.archived = false
  ORDER BY COALESCE(i.issue_id, i.id), i.created_at DESC, i.id DESC
) AS deduped
WHERE (deduped.created_at, deduped.id) < (sqlc.arg('cursor_created_at')::timestamptz, sqlc.arg('cursor_id')::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit');

-- name: GetInboxItem :one
SELECT * FROM inbox_item
WHERE id = $1;

-- name: GetInboxItemInWorkspace :one
SELECT * FROM inbox_item
WHERE id = $1 AND workspace_id = $2;

-- name: CreateInboxItem :one
INSERT INTO inbox_item (
    workspace_id, recipient_type, recipient_id,
    type, severity, issue_id, title, body,
    actor_type, actor_id, details
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: MarkInboxRead :one
UPDATE inbox_item SET read = true
WHERE id = $1
RETURNING *;

-- name: ArchiveInboxItem :one
UPDATE inbox_item SET archived = true
WHERE id = $1
RETURNING *;

-- name: ArchiveInboxByIssue :execrows
UPDATE inbox_item SET archived = true
WHERE workspace_id = $1 AND recipient_type = $2 AND recipient_id = $3 AND issue_id = $4 AND archived = false;

-- name: CountUnreadInbox :one
-- Count of distinct issues (or item-id for issueless items) with at least
-- one unread, unarchived inbox entry. Aligned with ListInboxItemsLatest's
-- per-issue dedup so the badge count matches the rendered list length.
SELECT COUNT(DISTINCT COALESCE(issue_id, id))::bigint FROM inbox_item
WHERE workspace_id = $1 AND recipient_type = $2 AND recipient_id = $3 AND read = false AND archived = false;

-- name: MarkAllInboxRead :execrows
UPDATE inbox_item SET read = true
WHERE workspace_id = $1 AND recipient_type = 'member' AND recipient_id = $2 AND archived = false AND read = false;

-- name: ArchiveAllInbox :execrows
UPDATE inbox_item SET archived = true
WHERE workspace_id = $1 AND recipient_type = 'member' AND recipient_id = $2 AND archived = false;

-- name: ArchiveAllReadInbox :execrows
UPDATE inbox_item SET archived = true
WHERE workspace_id = $1 AND recipient_type = 'member' AND recipient_id = $2 AND read = true AND archived = false;

-- name: ArchiveCompletedInbox :execrows
UPDATE inbox_item i SET archived = true
WHERE i.workspace_id = $1 AND i.recipient_type = 'member' AND i.recipient_id = $2 AND i.archived = false
  AND i.issue_id IN (SELECT id FROM issue WHERE status IN ('done', 'cancelled'));
