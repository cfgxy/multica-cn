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
-- NULL status lists every status; a set status filters to it. Row visibility
-- is workspace-membership scoped at the route: every member of this space
-- sees its rows; operator tiers gate who may ANSWER (the answer endpoint's
-- tier check), never who may see.
--
-- RUYI-864: `order` selects the scan direction — 'desc' (newest-first, the
-- history surface) or 'asc' (oldest-first, the authorization queue). The two
-- directions are parameterized with the CASE + NULLS LAST idiom so a single
-- query serves both without a second query (or a second slugs CTE) to keep in
-- sync: the inactive direction's columns are all NULL and sort last, leaving
-- the active direction's (created_at, id) pair fully in charge. 'asc' is what
-- makes the queue's "oldest pending first, next after processing" stable.
SELECT * FROM decision_requests
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY
  (CASE WHEN sqlc.narg('order')::text = 'desc' THEN created_at ELSE NULL END) DESC NULLS LAST,
  (CASE WHEN sqlc.narg('order')::text = 'desc' THEN id ELSE NULL END) DESC NULLS LAST,
  (CASE WHEN sqlc.narg('order')::text = 'asc' THEN created_at ELSE NULL END) ASC NULLS LAST,
  (CASE WHEN sqlc.narg('order')::text = 'asc' THEN id ELSE NULL END) ASC NULLS LAST
LIMIT $2;

-- name: ListAccessibleDecisionRequestTargetSlugs :many
-- Return target routes only when the requesting user is also a member of the
-- target workspace. This metadata is attached only to origin projections.
-- RUYI-864: the window mirrors the list's `order` direction, so the slugs
-- attached to an oldest-first page come from the same page's origin rows.
WITH visible_origin AS (
    SELECT request_row.request_group_id
    FROM decision_requests request_row
    WHERE request_row.workspace_id = $1
      AND request_row.role = 'origin'
      AND (sqlc.narg('status')::text IS NULL OR request_row.status = sqlc.narg('status')::text)
    ORDER BY
      (CASE WHEN sqlc.narg('order')::text = 'desc' THEN request_row.created_at ELSE NULL END) DESC NULLS LAST,
      (CASE WHEN sqlc.narg('order')::text = 'desc' THEN request_row.id ELSE NULL END) DESC NULLS LAST,
      (CASE WHEN sqlc.narg('order')::text = 'asc' THEN request_row.created_at ELSE NULL END) ASC NULLS LAST,
      (CASE WHEN sqlc.narg('order')::text = 'asc' THEN request_row.id ELSE NULL END) ASC NULLS LAST
    LIMIT $3
)
SELECT DISTINCT origin.request_group_id, target_workspace.slug
FROM visible_origin origin
JOIN decision_requests target
  ON target.request_group_id = origin.request_group_id
 AND target.role = 'target'
JOIN workspace target_workspace ON target_workspace.id = target.workspace_id
JOIN member target_member ON target_member.workspace_id = target.workspace_id
WHERE target_member.user_id = $2;

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
    -- decision_center: this endpoint (decision-center row buttons and the
    -- bare API) is its own channel, not a card click (migration 967).
    answer_source = 'decision_center',
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

-- ExpireDueDecisionRequests flips every group whose window lapsed before
-- all required steps approved (lazy expiry — no scheduler). A group
-- qualifies only while a pending step remains: a fully-approved group is
-- already past consent — its advance/execute pass just hasn't landed yet —
-- and expiring it there would void captured human approvals and misreport
-- the terminal callback. Groups with no pending row are otherwise terminal
-- and stay put.
-- name: ExpireDueDecisionRequests :many
WITH due_groups AS (
    SELECT request_group_id
    FROM decision_requests
    WHERE expires_at <= now() AND status IN ('pending', 'approved')
    GROUP BY request_group_id
    HAVING count(*) FILTER (WHERE status = 'pending') > 0
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

-- name: ClaimDecisionRequestIssueDoneReceipt :many
-- The target issue status transition is the idempotency fence for the second
-- (Done) receipt. The request-group execution transaction already persists
-- the single target issue id in execution_result.
WITH updated AS (
    UPDATE decision_requests SET
        execution_result = jsonb_set(
            execution_result,
            '{done_receipt}',
            jsonb_build_object('status', 'done', 'target_issue_id', @target_issue_id::text, 'completed_at', now()),
            true
        ),
        updated_at = now()
    WHERE action_type = 'single_issue_create'
      AND status = 'executed'
      AND execution_result->>'target_issue_id' = @target_issue_id::text
      AND execution_result->'done_receipt' IS NULL
    RETURNING *
), updated_cards AS (
    UPDATE issue_decisions SET
        execution_result = jsonb_set(
            execution_result,
            '{done_receipt}',
            jsonb_build_object('status', 'done', 'target_issue_id', @target_issue_id::text, 'completed_at', now()),
            true
        ),
        updated_at = now()
    WHERE pending_request_group_id IN (
        SELECT request_group_id FROM updated WHERE role = 'origin'
    )
      AND pending_action_type = 'single_issue_create'
      AND auth_state = 'executed'
    RETURNING id
)
SELECT * FROM updated WHERE role = 'origin';

-- name: CreateDecisionRequestActionSavepoint :exec
SAVEPOINT decision_request_action;

-- name: RollbackDecisionRequestAction :exec
ROLLBACK TO SAVEPOINT decision_request_action;

-- name: ReleaseDecisionRequestActionSavepoint :exec
RELEASE SAVEPOINT decision_request_action;
