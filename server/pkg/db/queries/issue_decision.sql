-- name: CreateIssueDecision :one
INSERT INTO issue_decisions (
    id, workspace_id, issue_id, source_comment_id,
    question, options, multi_select, recommended_indices,
    created_by_type, created_by_id
) VALUES (
    @id, @workspace_id, @issue_id, @source_comment_id,
    @question, @options::jsonb, @multi_select, @recommended_indices::jsonb,
    @created_by_type, @created_by_id
)
RETURNING *;

-- name: GetIssueDecision :one
SELECT * FROM issue_decisions
WHERE id = $1 AND workspace_id = $2;

-- name: ListIssueDecisionsForIssue :many
SELECT * FROM issue_decisions
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY created_at ASC, id ASC;

-- name: AnswerIssueDecision :one
-- CAS on status: only an open card can be answered. A concurrent answer (or a
-- cancel racing an answer) loses here with sql.ErrNoRows instead of silently
-- overwriting the first decision.
UPDATE issue_decisions SET
    status = 'answered',
    selected_indices = @selected_indices::jsonb,
    answered_by_type = @answered_by_type,
    answered_by_id = @answered_by_id,
    answered_at = now(),
    answer_comment_id = @answer_comment_id,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'open'
RETURNING *;

-- name: CancelIssueDecision :one
-- Same CAS discipline as AnswerIssueDecision: the first of {answer, cancel}
-- to flip an open card wins; the loser gets sql.ErrNoRows.
UPDATE issue_decisions SET
    status = 'cancelled',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'open'
RETURNING *;

-- name: SetIssueDecisionAnswerComment :one
-- Links the answer echo comment to an already-won card and returns the
-- updated row, so the handler's response reflects the link. The answer CAS
-- must win before this runs; it is a pointer fill, so no status guard.
UPDATE issue_decisions SET
    answer_comment_id = @answer_comment_id,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;


-- name: CountWorkspaceIssueDecisionsByStatus :one
-- Section counts for the workspace decision inbox (RUYI-494). One scan
-- serves all three counts; the caller scopes by workspace_id, which the
-- member middleware has already authorized.
SELECT
    count(*) FILTER (WHERE status = 'open')::int AS open_count,
    count(*) FILTER (WHERE status = 'answered')::int AS answered_count,
    count(*) FILTER (WHERE status = 'cancelled')::int AS cancelled_count
FROM issue_decisions
WHERE workspace_id = $1;

-- name: ListWorkspaceIssueDecisions :many
-- Workspace decision inbox rows (RUYI-494). One row PER CARD — issues are
-- context (identifier + title for the list's first line), never a
-- grouping/dedup unit: an issue with three cards yields three rows so old
-- open cards can't be hidden by a newer one. NULL status lists every
-- status; a set status filters to it. Ordered newest-first; the client
-- groups by status with open first.
SELECT d.*,
       i.number AS issue_number,
       i.title AS issue_title,
       COALESCE(ws.issue_prefix || '-' || i.number::text, '')::text AS issue_identifier
FROM issue_decisions d
JOIN issue i ON i.id = d.issue_id
LEFT JOIN workspace ws ON ws.id = d.workspace_id
WHERE d.workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR d.status = sqlc.narg('status')::text)
ORDER BY d.created_at DESC, d.id DESC
LIMIT $2;
