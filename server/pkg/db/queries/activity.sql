-- name: ListActivitiesLatest :many
-- Top N activities for an issue, newest first. Used by the cursor-paginated
-- timeline endpoint to assemble the latest page.
SELECT * FROM activity_log
WHERE issue_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListActivitiesBefore :many
SELECT * FROM activity_log
WHERE issue_id = $1
  AND (created_at, id) < ($2::timestamptz, $3::uuid)
ORDER BY created_at DESC, id DESC
LIMIT $4;

-- name: ListActivitiesAfter :many
SELECT * FROM activity_log
WHERE issue_id = $1
  AND (created_at, id) > ($2::timestamptz, $3::uuid)
ORDER BY created_at ASC, id ASC
LIMIT $4;

-- name: ListActivitiesInRange :many
-- Activities for an issue within an inclusive [lower, upper] time range,
-- newest first. Backs the V2 timeline pagination where comments anchor each
-- page and activities ride along in the page's time window. Limit acts as a
-- hard cap; clients infer truncation via "limit + 1" or a separate count.
SELECT * FROM activity_log
WHERE issue_id = $1
  AND created_at >= $2::timestamptz
  AND created_at <= $3::timestamptz
ORDER BY created_at DESC, id DESC
LIMIT $4;

-- name: ListActivitiesSince :many
-- Activities for an issue with created_at >= lower bound (no upper bound),
-- newest first. Used by V2 latest mode so activities posted after the newest
-- comment still belong to the first page rather than disappearing into a
-- non-existent "newer" bucket.
SELECT * FROM activity_log
WHERE issue_id = $1
  AND created_at >= $2::timestamptz
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListActivitiesInBeforeWindow :many
-- Activities for V2 before-mode: keyset upper bound (exclusive) so activities
-- already shown on the previous page don't double-count, plus an inclusive
-- lower-bound timestamp so the page only carries the slice between the
-- previous boundary and this page's oldest comment.
SELECT * FROM activity_log
WHERE issue_id = $1
  AND (created_at, id) < ($2::timestamptz, $3::uuid)
  AND created_at >= $4::timestamptz
ORDER BY created_at DESC, id DESC
LIMIT $5;

-- name: GetActivity :one
-- Used by the around-id mode of ListTimeline to resolve an entry to its
-- (created_at, id) cursor when the entry is an activity.
SELECT * FROM activity_log
WHERE id = $1;

-- name: CreateActivity :one
INSERT INTO activity_log (
    workspace_id, issue_id, actor_type, actor_id, action, details
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CountAssigneeChangesByActor :many
-- Count how many times a user assigned each target via assignee_changed activities.
SELECT
  details->>'to_type' as assignee_type,
  details->>'to_id' as assignee_id,
  COUNT(*)::bigint as frequency
FROM activity_log
WHERE workspace_id = $1
  AND actor_id = $2
  AND actor_type = 'member'
  AND action = 'assignee_changed'
  AND details->>'to_type' IS NOT NULL
  AND details->>'to_id' IS NOT NULL
GROUP BY details->>'to_type', details->>'to_id';
