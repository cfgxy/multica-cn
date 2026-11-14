-- RUYI-941: Quiz scheduler control plane. Missing rows mean enabled.

-- name: LockWorkspaceForPromptQuizSchedule :one
-- Every Quiz control write takes the workspace lock before reading or changing
-- the override. Quiz task creation takes the compatible KEY SHARE lock in a
-- separate transaction command, so the insert's snapshot is fresh after a
-- concurrent disable commits.
SELECT id
FROM workspace
WHERE id = @workspace_id
FOR UPDATE;

-- name: GetPromptQuizScheduleControlForWorkspace :one
SELECT *
FROM prompt_quiz_schedule_control
WHERE workspace_id = @workspace_id
  AND agent_id IS NULL;

-- name: GetPromptQuizScheduleControlForAgent :one
-- Most-specific override wins: an agent row shadows the workspace row.
SELECT *
FROM prompt_quiz_schedule_control
WHERE workspace_id = @workspace_id
  AND (agent_id IS NULL OR agent_id = @agent_id)
ORDER BY agent_id NULLS LAST
LIMIT 1;

-- name: UpsertPromptQuizScheduleControl :one
INSERT INTO prompt_quiz_schedule_control (
    workspace_id, agent_id, enabled, reason, updated_by, updated_at
) VALUES (
    @workspace_id, @agent_id, @enabled, @reason, @updated_by, now()
)
ON CONFLICT (workspace_id) WHERE agent_id IS NULL
DO UPDATE SET
    enabled = EXCLUDED.enabled,
    reason = EXCLUDED.reason,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
WHERE @agent_id::uuid IS NULL
RETURNING *;

-- name: UpsertPromptQuizScheduleControlForAgent :one
INSERT INTO prompt_quiz_schedule_control (
    workspace_id, agent_id, enabled, reason, updated_by, updated_at
) VALUES (
    @workspace_id, @agent_id, @enabled, @reason, @updated_by, now()
)
ON CONFLICT (workspace_id, agent_id) WHERE agent_id IS NOT NULL
DO UPDATE SET
    enabled = EXCLUDED.enabled,
    reason = EXCLUDED.reason,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: DeletePromptQuizScheduleControl :execrows
DELETE FROM prompt_quiz_schedule_control
WHERE workspace_id = @workspace_id
  AND agent_id IS NOT DISTINCT FROM @agent_id;

-- name: CancelQueuedPromptQuizTasksForScheduleScope :many
-- Disable is a control-plane action, not data deletion. Queued and pre-start
-- dispatched rows are terminalised; running rows are deliberately left alone so
-- the daemon can finish and the collector can retain their measurements.
UPDATE agent_task_queue atq
SET status = 'cancelled',
    completed_at = now(),
    cancel_reason = 'prompt_quiz_schedule_disabled',
    cancel_actor_type = 'system',
    prepare_lease_expires_at = NULL
FROM agent a
WHERE atq.agent_id = a.id
  AND a.workspace_id = @workspace_id
  AND (@agent_id::uuid IS NULL OR atq.agent_id = @agent_id)
  AND atq.originator_source = 'quiz'
  AND atq.issue_id IS NULL
  -- A workspace disable must not cancel an explicit enabled agent override.
  -- Resolve the same most-specific row used by the sweep and claim fence.
  AND COALESCE((
      SELECT c.enabled
      FROM prompt_quiz_schedule_control c
      WHERE c.workspace_id = a.workspace_id
        AND (c.agent_id IS NULL OR c.agent_id = a.id)
      ORDER BY c.agent_id NULLS LAST
      LIMIT 1
  ), TRUE) = FALSE
  AND atq.status IN ('queued', 'dispatched', 'waiting_local_directory', 'deferred')
RETURNING atq.*;

-- name: CountPromptQuizTasksForScheduleScope :one
SELECT
    count(*) FILTER (WHERE atq.status IN ('queued', 'dispatched', 'waiting_local_directory', 'deferred'))::bigint AS queued_count,
    count(*) FILTER (WHERE atq.status = 'running')::bigint AS running_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = @workspace_id
  AND (@agent_id::uuid IS NULL OR atq.agent_id = @agent_id)
  AND atq.originator_source = 'quiz'
  AND atq.issue_id IS NULL;
