-- name: CreateIssueDecision :one
-- RUYI-865: the insert is refused in one atomic statement when the issue
-- sits in the done category (built-in 'done' or a custom done key resolved
-- through the status catalog): a card raised after the done transition has
-- no cascade left to clear it, so it must not enter the backlog. The guard
-- makes the create a no-op RETURNING 0 rows (sql.ErrNoRows) in that case;
-- the handler maps the miss onto the 409 contract.
INSERT INTO issue_decisions (
    id, workspace_id, issue_id, source_comment_id,
    question, context, options, multi_select, recommended_indices,
    created_by_type, created_by_id, client_request_id
)
SELECT
    @id::uuid, @workspace_id::uuid, @issue_id::uuid, @source_comment_id::uuid,
    @question::text, @context::jsonb, @options::jsonb, @multi_select::bool, @recommended_indices::jsonb,
    @created_by_type::text, @created_by_id::uuid, @client_request_id
WHERE NOT EXISTS (
    SELECT 1 FROM issue i
    WHERE i.id = @issue_id::uuid AND i.workspace_id = @workspace_id::uuid
      AND (
        i.status = 'done'
        OR EXISTS (
            SELECT 1 FROM issue_status s
            WHERE s.workspace_id = i.workspace_id
              AND s.key = i.status
              AND s.category = 'done'
        )
      )
)
RETURNING *;

-- name: LockIssueForDecisionCreate :one
-- RUYI-865: the create-side half of the done-race lock protocol. Taking the
-- issue row FOR UPDATE inside the card-create transaction serialises the
-- insert's done guard against every formal done status write. A done status
-- write is a plain `UPDATE issue SET status = ...`, which locks the same
-- row (FOR UPDATE / ROW EXCLUSIVE, or at least FOR NO KEY UPDATE when the
-- table has no unique constraint); FOR UPDATE conflicts with BOTH, so the
-- two writers cannot interleave on the row regardless of the table's
-- constraints. Outcomes:
--   - the done write commits first -> the guarded insert's post-lock re-read
--     (a fresh READ COMMITTED snapshot once the lock is granted) sees done
--     and the INSERT ... WHERE NOT EXISTS yields 0 rows -> the handler maps
--     it to a 409;
--   - the card commits first -> the done write's UPDATE is still queued
--     behind the FOR UPDATE lock, so once it lands its post-commit cancel
--     batch (a fresh READ COMMITTED snapshot) sees the just-committed card
--     and cancels it.
-- Either way no open question card survives a done transition. Mirrors
-- LockIssueForDelete (issue.sql): the side that must not slip past the
-- writer holds the exclusive row lock the writer conflicts with. Lock
-- order is the single issue row, so no deadlock cycle is possible.
SELECT id FROM issue
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

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

-- name: ListIssueDecisionsByAnswerComments :many
-- RUYI-719: typed claim-time answer context. Locates the question cards whose
-- answer echo is one of the comments delivered with this claim (trigger +
-- coalesced). Authorization cards never take the echo pointer (their terminal
-- callback is a system comment) and are excluded explicitly by kind, so a
-- future pointer fill can never leak approve/deny answers into this
-- question-card surface. Deterministic order keeps a retried claim's payload
-- byte-stable.
SELECT * FROM issue_decisions
WHERE workspace_id = $1 AND issue_id = $2
  AND decision_kind = 'question'
  AND answer_comment_id = ANY(@answer_comment_ids::uuid[])
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

-- name: CancelOpenIssueDecisionsForIssue :many
-- RUYI-865: when an issue formally enters the done category, every one of
-- its still-open question cards is auto-cancelled in a single CAS batch so
-- it cannot re-enter the decision-center backlog. The status guard makes
-- the batch idempotent (re-running on an already-cleaned issue is a no-op)
-- and races an in-flight answer safely: whichever of {answer, cancel} wins
-- the open->terminal flip first leaves a consistent row, and only cards
-- actually flipped are returned so events and audit rows cover exactly
-- what changed. Authorization cards keep their request-owned lifecycle
-- (RUYI-630 decision_requests surface) and are excluded here on purpose.
UPDATE issue_decisions SET
    status = 'cancelled',
    updated_at = now()
WHERE issue_id = @issue_id AND workspace_id = @workspace_id
  AND status = 'open'
  AND decision_kind = 'question'
RETURNING *;

-- name: RestoreIssueDecision :one
-- RUYI-863: the inverse CAS of CancelIssueDecision — only a cancelled card
-- on a non-terminal issue can be restored to open. An answered card is
-- never revivable (sql.ErrNoRows here). The terminal-issue predicate is the
-- write-side half of the "no illegal resurrection" rule: it mirrors
-- issuestatus.Effective exactly — built-in done/cancelled are terminal keys
-- on their own (unseeded workspaces have no catalog rows for them, so the
-- key set is checked directly), and a custom status is terminal when its
-- catalog category is done/cancelled. Effective ignores archived entries on
-- purpose (issues still on them keep their behavior), so this predicate does
-- too.
--
-- The predicate runs under a FOR UPDATE lock on the card's owning issue row,
-- which is the linearization point for the terminal-write race. The plain
-- snapshot predicate this used to rely on was not an atomic guard: the CTE's
-- plain read of the issue row saw whatever snapshot it had, while a
-- concurrent UpdateIssueStatus commits its own terminal write independently,
-- so a restore that read "in_progress" could still commit an open card on an
-- issue that was already done (RUYI-863 P1). With the lock, the issue row
-- serializes both sides: a terminal write that commits first is held out of
-- the row until this statement finishes, and this statement's CTE — run
-- second — re-reads the row under its own lock, sees the committed terminal
-- version, yields zero rows, and the UPDATE flips nothing. A restore that
-- wins the lock first is the legal boundary: its card goes open and the
-- later terminal write simply lands behind it.
WITH locked_issue AS MATERIALIZED (
    -- Admit the restore only when the locked issue row is still
    -- non-terminal; the MATERIALIZED CTE plus the UPDATE's FROM clause force
    -- the lock before the card flip. Mirrors the global issue -> child
    -- lock order used by issue teardown and comment writes, so no
    -- deadlock cycle against those paths.
    SELECT i.id
    FROM issue i
    JOIN issue_decisions d ON d.issue_id = i.id
                            AND d.workspace_id = i.workspace_id
    WHERE d.id = $1
      AND i.status NOT IN ('done', 'cancelled')
      AND NOT EXISTS (
          SELECT 1 FROM issue_status s
          WHERE s.workspace_id = i.workspace_id
            AND s.key = i.status
            AND s.category IN ('done', 'cancelled')
      )
    FOR UPDATE OF i
)
UPDATE issue_decisions SET
    status = 'open',
    updated_at = now()
FROM locked_issue
WHERE issue_decisions.id = $1
  AND issue_decisions.workspace_id = $2
  AND issue_decisions.status = 'cancelled'
RETURNING issue_decisions.*;

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
-- member middleware has already authorized. RUYI-630: authorization cards
-- stay out of the inbox aggregation entirely — the item filter in the
-- handler drops them, and these counts must agree with the items they
-- badge, so they exclude the authorization kind at the source.
SELECT
    count(*) FILTER (WHERE status = 'open')::int AS open_count,
    count(*) FILTER (WHERE status = 'answered')::int AS answered_count,
    count(*) FILTER (WHERE status = 'cancelled')::int AS cancelled_count
FROM issue_decisions
WHERE workspace_id = $1
  AND decision_kind = 'question';

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
-- was missed by the same-transaction sweep still converges here). A group
-- whose approval is fully captured — every row approved or already past
-- execution — keeps its card: the window lapsed before execution landed,
-- not before consent did (same guard as ExpireDueDecisionRequests).
UPDATE issue_decisions SET
    auth_state = 'expired',
    updated_at = now()
WHERE decision_kind = 'authorization'
  AND status = 'open' AND auth_state = 'pending'
  AND expires_at IS NOT NULL AND expires_at <= now()
  AND EXISTS (
      SELECT 1 FROM decision_requests dr
      WHERE dr.request_group_id = issue_decisions.pending_request_group_id
        AND dr.status NOT IN ('approved', 'executed')
  )
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
