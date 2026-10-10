-- name: CreateIssueDecision :one
INSERT INTO issue_decisions (
    id, workspace_id, issue_id, source_comment_id,
    question, options, multi_select, recommended_indices,
    created_by_type, created_by_id, client_request_id
) VALUES (
    @id, @workspace_id, @issue_id, @source_comment_id,
    @question, @options::jsonb, @multi_select, @recommended_indices::jsonb,
    @created_by_type, @created_by_id, @client_request_id
)
RETURNING *;

-- name: GetIssueDecision :one
SELECT * FROM issue_decisions
WHERE id = $1 AND workspace_id = $2;

-- name: GetIssueDecisionByIdempotencyKey :one
-- Replay lookup for RUYI-514: fires only after the unique index
-- uidx_issue_decisions_client_request rejected a duplicate insert, so the
-- winning row is already committed and visible (READ COMMITTED). Key scope
-- mirrors the index: workspace + creator type + creator id.
SELECT * FROM issue_decisions
WHERE workspace_id = $1 AND created_by_type = $2 AND created_by_id = $3
  AND client_request_id = $4;

-- name: ListIssueDecisionsForIssue :many
SELECT * FROM issue_decisions
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY created_at ASC, id ASC;

-- name: AnswerIssueDecision :one
-- CAS on status: only an open card can be answered. A concurrent answer (or a
-- cancel racing an answer) loses here with sql.ErrNoRows instead of silently
-- overwriting the first decision. RUYI-630 adds the structured answer_source
-- the server stamps per channel (card_click / text_token / batch); the field
-- is record-only on question cards but is the authorization-grade truth on
-- authorization cards, which answer through AnswerAuthorizationDecisionCard.
UPDATE issue_decisions SET
    status = 'answered',
    selected_indices = @selected_indices::jsonb,
    answered_by_type = @answered_by_type,
    answered_by_id = @answered_by_id,
    answered_at = now(),
    answer_comment_id = @answer_comment_id,
    answer_source = @answer_source::text,
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

-- name: AnswerAuthorizationDecisionCard :one
-- RUYI-630: CAS for the authorization link card. Distinct from
-- AnswerIssueDecision so the question-card CAS stays byte-for-byte on its
-- old semantics: this one additionally requires the authorization face to
-- still be pending and inside its window, and records the structured
-- answer_source. Index 0 = approve, index 1 = deny (options carry the
-- custom labels); auth_state lands approved/denied accordingly.
UPDATE issue_decisions SET
    status = 'answered',
    auth_state = @auth_state::text,
    selected_indices = @selected_indices::jsonb,
    answered_by_type = 'member',
    answered_by_id = @answered_by_id,
    answered_at = now(),
    answer_source = 'card_click',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
  AND status = 'open' AND auth_state = 'pending'
  AND (expires_at IS NULL OR expires_at > now())
RETURNING *;

-- name: SyncDecisionCardsForGroup :many
-- RUYI-630: propagate a group-level state onto the authorization link cards
-- of that group. For pending cards auth_state follows the group state;
-- execution outcome fields ride along. Cards already answered by their own
-- step (auth_state approved/denied) are left untouched except that the
-- caller passes execution outcomes separately when needed.
UPDATE issue_decisions SET
    auth_state = @auth_state::text,
    executed_at = sqlc.narg('executed_at'),
    execution_result = sqlc.narg('execution_result'),
    execution_error = sqlc.narg('execution_error'),
    updated_at = now()
WHERE pending_request_group_id = @request_group_id
  AND status = 'open' AND auth_state = 'pending'
RETURNING *;

-- name: ExpireDueDecisionCards :many
-- RUYI-630: lazy expiry for authorization link cards (standalone expiry
-- guard mirroring the group sweep; a card whose group row expired but that
-- was missed by the same-transaction sweep still converges here).
UPDATE issue_decisions SET
    auth_state = 'expired',
    updated_at = now()
WHERE decision_kind = 'authorization'
  AND status = 'open' AND auth_state = 'pending'
  AND expires_at IS NOT NULL AND expires_at <= now()
RETURNING *;

-- name: SetAuthorizationCardExecution :many
-- RUYI-630: write the executor outcome onto the authorization link cards of
-- a group (all of them — every reader sees the same execution truth).
UPDATE issue_decisions SET
    auth_state = @auth_state::text,
    executed_at = sqlc.narg('executed_at'),
    execution_result = sqlc.narg('execution_result'),
    execution_error = sqlc.narg('execution_error'),
    updated_at = now()
WHERE pending_request_group_id = @request_group_id
RETURNING *;

-- name: CreateAuthorizationDecisionCard :one
-- RUYI-630: the issue-thread authorization link card. Created only by the
-- decision-request flow (the public card-create endpoint stays
-- question-only); options are exactly [approve, deny] with the custom
-- labels baked in.
INSERT INTO issue_decisions (
    id, workspace_id, issue_id, source_comment_id,
    question, options, multi_select, recommended_indices,
    decision_kind, visible_tier, operator_tier, named_approver_ids,
    approve_label, deny_label, expires_at, auth_state,
    pending_request_group_id, pending_action_type, pending_action_params,
    created_by_type, created_by_id
) VALUES (
    @id, @workspace_id, @issue_id, @source_comment_id,
    @question, @options::jsonb, false, '[0,1]'::jsonb,
    'authorization', @visible_tier::text, @operator_tier::text, @named_approver_ids::jsonb,
    @approve_label, @deny_label, @expires_at, 'pending',
    @request_group_id, @action_type, @action_params::jsonb,
    @created_by_type, @created_by_id
)
RETURNING *;

-- name: GetAuthorizationCardForGroup :one
-- RUYI-630: the link card of a group in one workspace (the origin step's
-- surface). Used by the decision-request detail summary; at most one card
-- per group per workspace.
SELECT * FROM issue_decisions
WHERE pending_request_group_id = @request_group_id
  AND workspace_id = @workspace_id
ORDER BY created_at DESC
LIMIT 1;
