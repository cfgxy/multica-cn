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

