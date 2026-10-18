-- name: CreateIssueSuspension :one
-- RUYI-690. The partial unique index uidx_issue_suspension_active_per_issue
-- backstops the handler's pre-check: a second active row for the same issue
-- loses here with the unique-violation error instead of double-suspending.
INSERT INTO issue_suspension (
    id, workspace_id, issue_id,
    reason, waiting_on_type, waiting_on_issue_id, waiting_on_decision_id,
    waiting_on_summary, resume_condition, resume_predicate,
    suspended_by_type, suspended_by_id
) VALUES (
    @id, @workspace_id, @issue_id,
    @reason, @waiting_on_type,
    @waiting_on_issue_id, @waiting_on_decision_id,
    @waiting_on_summary, @resume_condition, @resume_predicate::jsonb,
    @suspended_by_type, @suspended_by_id
)
RETURNING *;

-- name: GetIssueSuspension :one
SELECT * FROM issue_suspension
WHERE id = $1 AND workspace_id = $2;

-- name: GetActiveIssueSuspensionForIssue :one
-- The current hold, if any. More than one active row is impossible by the
-- partial unique index, so :one is exact, not a LIMIT 1.
SELECT * FROM issue_suspension
WHERE issue_id = $1 AND status = 'active';

-- name: ListIssueSuspensionsForIssue :many
-- Full history for the issue detail surface, newest first.
SELECT * FROM issue_suspension
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY suspended_at DESC, id DESC;

-- name: ListWorkspaceIssueSuspensions :many
-- Workspace suspension aggregate view (RUYI-690). One row PER SUSPENSION —
-- the issue is context (identifier, title, lifecycle status), never a
-- grouping unit. NULL status lists every status; a set status filters to it
-- (the aggregate view lists actives, the client can ask for history later).
-- Waiting-target columns are denormalized into the row so the view renders
-- "waiting on RUYI-668 (in_progress)" / "waiting on decision '…?' (open)"
-- with zero follow-up round-trips.
SELECT s.*,
       i.number AS issue_number,
       i.title AS issue_title,
       i.status AS issue_status,
       i.suspended_at AS issue_suspended_at,
       COALESCE(ws.issue_prefix || '-' || i.number::text, '')::text AS issue_identifier,
       wi.number AS waiting_issue_number,
       wi.title AS waiting_issue_title,
       wi.status AS waiting_issue_status,
       COALESCE(ws.issue_prefix || '-' || wi.number::text, '')::text AS waiting_issue_identifier,
       d.question AS waiting_decision_question,
       d.status AS waiting_decision_status
FROM issue_suspension s
JOIN issue i ON i.id = s.issue_id
LEFT JOIN workspace ws ON ws.id = s.workspace_id
LEFT JOIN issue wi ON wi.id = s.waiting_on_issue_id
LEFT JOIN issue_decisions d ON d.id = s.waiting_on_decision_id
WHERE s.workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR s.status = sqlc.narg('status')::text)
ORDER BY s.suspended_at DESC, s.id DESC
LIMIT $2;

-- name: CountWorkspaceIssueSuspensions :one
-- Badge/count for the aggregate view entry point. Active-only: history is
-- not a queue anyone works down.
SELECT count(*)::int FROM issue_suspension
WHERE workspace_id = $1 AND status = 'active';

-- name: ResumeIssueSuspension :one
-- CAS on status: only an active row can be resumed. A concurrent resume (or
-- a cancel racing a resume, or the system resume firing while a member
-- clicks) loses here with sql.ErrNoRows instead of double-writing the audit.
-- The issue.suspended_at snapshot is cleared by the caller in the same
-- transaction.
UPDATE issue_suspension SET
    status = 'resumed',
    resumed_by_type = @resumed_by_type,
    resumed_by_id = @resumed_by_id,
    resumed_at = now(),
    resume_note = @resume_note,
    resumed_by_predicate = @resumed_by_predicate::jsonb
WHERE id = @id AND workspace_id = @workspace_id AND status = 'active'
RETURNING *;

-- name: CancelIssueSuspension :one
-- Same CAS discipline as ResumeIssueSuspension: the first of
-- {resume, cancel} to flip the active row wins; the loser gets
-- sql.ErrNoRows. Cancel records that the suspension was withdrawn as
-- mistaken/obsolete — distinct from resumed, which asserts the wait ended.
UPDATE issue_suspension SET
    status = 'cancelled',
    cancelled_by_type = @cancelled_by_type,
    cancelled_by_id = @cancelled_by_id,
    cancelled_at = now(),
    cancel_note = @cancel_note
WHERE id = @id AND workspace_id = @workspace_id AND status = 'active'
RETURNING *;

-- name: ListActiveSuspensionsWaitingOnIssue :many
-- Auto-resume scan for the issue_done predicate (RUYI-690). waiting_on_issue_id
-- is authoritative — the application layer forces predicate kind and the
-- waiting_on_type CHECK to agree with it — so the join-free column drives the
-- scan. Rows returned are CAS-resumed one by one; losers of a concurrent
-- resume are skipped by the caller.
SELECT * FROM issue_suspension
WHERE status = 'active' AND waiting_on_issue_id = $1
ORDER BY suspended_at ASC;

-- name: ListActiveSuspensionsWaitingOnDecision :many
-- Auto-resume scan for the decision_answered predicate. Fires on answer
-- only: a cancelled card withdrew the question without granting it, so the
-- waiter stays suspended for a human to resolve.
SELECT * FROM issue_suspension
WHERE status = 'active' AND waiting_on_decision_id = $1
ORDER BY suspended_at ASC;

-- name: SetIssueSuspendedAt :one
-- Read-side snapshot maintenance (RUYI-690), same discipline as
-- SetIssueRunSuppressed (RUYI-275): deliberately does NOT touch updated_at /
-- revision / last_activity_at — this is server-side state maintenance, not a
-- user-visible issue edit. The dedicated issue_suspension:updated WS event
-- carries the refreshed snapshot so boards badge the hold without a refetch.
UPDATE issue SET
    suspended_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearIssueSuspendedAt :one
UPDATE issue SET
    suspended_at = NULL
WHERE id = $1
RETURNING *;
