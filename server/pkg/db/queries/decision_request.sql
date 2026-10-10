-- name: CreateDecisionRequest :one
INSERT INTO decision_requests (
    id, request_group_id, workspace_id, role, operable,
    status, action_type, action_params, risk_tier,
    title, detail,
    origin_workspace_id, origin_agent_id, origin_task_id,
    origin_issue_id, origin_issue_title,
    operator_tier, named_approver_ids, approve_label, deny_label,
    expires_at, created_by_type, created_by_id
) VALUES (
    @id, @request_group_id, @workspace_id, @role::text, @operable,
    'pending', @action_type, @action_params::jsonb, @risk_tier::text,
    @title, @detail,
    @origin_workspace_id, @origin_agent_id, @origin_task_id,
    @origin_issue_id, @origin_issue_title,
    @operator_tier::text, @named_approver_ids::jsonb, @approve_label, @deny_label,
    @expires_at, 'agent', @created_by_id
)
RETURNING *;

-- name: GetDecisionRequest :one
SELECT * FROM decision_requests
WHERE id = $1 AND workspace_id = $2;

-- name: GetDecisionRequestGroup :many
SELECT * FROM decision_requests
WHERE request_group_id = $1
ORDER BY created_at ASC, id ASC;

-- name: ListDecisionRequestsForWorkspace :many
-- Decision-center rows for one workspace (one row PER SPACE-LEVEL REQUEST).
-- NULL status lists every status; a set status filters to it. Visibility
-- (restricted tiers) is enforced by the handler after the fetch — the row
-- carries everything the check needs.
SELECT * FROM decision_requests
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: CountWorkspaceDecisionRequestsByStatus :one
-- Section counts for the decision-center authorization section. One scan
-- serves all three counts; the caller scopes by workspace_id.
SELECT
    count(*) FILTER (WHERE status IN ('pending', 'approved'))::int AS pending_count,
    count(*) FILTER (WHERE status IN ('denied', 'expired', 'revoked', 'execute_failed'))::int AS closed_count,
    count(*) FILTER (WHERE status = 'executed')::int AS executed_count
FROM decision_requests
WHERE workspace_id = $1;

-- AnswerDecisionRequest is the CAS for an OPERABLE row (target row, or an
-- origin row with no issue reference). Read-only projections never win this
-- CAS — the operable column is part of the guard, so a projected row answer
-- reports "no longer actionable" instead of silently granting. The lazy
-- expiry guard lives in the expires_at predicate.
-- name: AnswerDecisionRequest :one
UPDATE decision_requests SET
    status = @decision::text,
    answered_by_type = 'member',
    answered_by_id = @answered_by_id,
    answered_at = now(),
    answer_source = 'card_click',
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
  AND operable AND status = 'pending' AND expires_at > now()
RETURNING *;

-- SyncDecisionRequestRowAnswer records an origin-step answer taken on the
-- issue_decisions authorization link card onto the origin projection row
-- (same transaction as the card update). Only a still-pending row moves.
-- name: SyncOriginDecisionRequestRowAnswer :one
-- RUYI-630: mirror a link-card answer onto the group's origin projection
-- row (发起空间第一步授权走卡片面，行只是状态投影). The pending guard
-- makes a late double answer a no-op; the advance pass owns everything
-- after.
UPDATE decision_requests SET
    status = @decision::text,
    answered_by_type = 'member',
    answered_by_id = @answered_by_id,
    answered_at = now(),
    answer_source = 'card_click',
    updated_at = now()
WHERE request_group_id = @request_group_id AND role = 'origin' AND status = 'pending'
RETURNING *;

-- PropagateDecisionRequestGroup moves a GROUP-level terminal state onto every
-- row still in flight (denied / expired / revoked / executed /
-- execute_failed). Execution outcome fields ride along so every space's
-- projection shows the same terminal truth (同事务双写同步).
-- name: PropagateDecisionRequestGroup :many
UPDATE decision_requests SET
    status = @status::text,
    executed_at = sqlc.narg('executed_at'),
    execution_result = sqlc.narg('execution_result'),
    execution_error = sqlc.narg('execution_error'),
    updated_at = now()
WHERE request_group_id = @request_group_id
  AND status IN ('pending', 'approved')
RETURNING *;

-- ExpireDueDecisionRequests flips every group whose window lapsed before all
-- required steps approved (lazy expiry — no scheduler). A group qualifies
-- only while ALL its rows are still in flight; a denied or executed group is
-- already terminal and stays put.
-- name: ExpireDueDecisionRequests :many
WITH due_groups AS (
    SELECT request_group_id
    FROM decision_requests
    WHERE expires_at <= now() AND status IN ('pending', 'approved')
    GROUP BY request_group_id
    HAVING count(*) FILTER (WHERE status NOT IN ('pending', 'approved')) = 0
)
UPDATE decision_requests SET status = 'expired', updated_at = now()
WHERE request_group_id IN (SELECT request_group_id FROM due_groups)
  AND status IN ('pending', 'approved')
RETURNING *;

-- CancelDecisionRequestGroup revokes every in-flight row of a group (creator
-- agent or a workspace Owner). The CAS predicate skips already-terminal
-- groups; the caller decides between "revoked" and "no longer revocable".
-- name: CancelDecisionRequestGroup :many
UPDATE decision_requests SET status = 'revoked', updated_at = now()
WHERE request_group_id = @request_group_id
  AND status IN ('pending', 'approved')
RETURNING *;

-- name: GetDecisionRequestGroupForUpdate :many
-- Row-locked group read for the advance/execute path: two step approvals
-- racing the last step serialize here, and the loser sees the group already
-- terminal instead of executing the action twice.
SELECT * FROM decision_requests
WHERE request_group_id = $1
ORDER BY created_at ASC, id ASC
FOR UPDATE;

-- name: MarkDecisionRequestCallbackDone :exec
UPDATE decision_requests SET terminal_callback_at = now(), updated_at = now()
WHERE request_group_id = $1;

-- name: SetDecisionRequestExecution :many
-- Writes the executor outcome onto every row of the group (same truth in
-- every space's projection) regardless of the row's current in-flight
-- status — the caller holds the group lock.
UPDATE decision_requests SET
    status = @status::text,
    executed_at = sqlc.narg('executed_at'),
    execution_result = sqlc.narg('execution_result'),
    execution_error = sqlc.narg('execution_error'),
    updated_at = now()
WHERE request_group_id = @request_group_id
RETURNING *;
