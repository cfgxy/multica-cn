-- name: GetIssueCoordinatorSession :one
SELECT issue_id, leader_agent_id, runtime_id, session_id, generation,
       snapshot_version, snapshot, last_event_sequence, active_task_id,
       last_task_id, continuity_state, updated_at
FROM issue_coordinator_session
WHERE issue_id = sqlc.arg(issue_id)
  AND leader_agent_id = sqlc.arg(leader_agent_id);

-- name: ClaimIssueCoordinatorSession :one
INSERT INTO issue_coordinator_session (
    issue_id, leader_agent_id, runtime_id, active_task_id, continuity_state,
    generation, session_id, snapshot_version, snapshot, last_event_sequence
)
VALUES (
    sqlc.arg(issue_id), sqlc.arg(leader_agent_id), sqlc.arg(runtime_id),
    sqlc.arg(task_id), 'fresh', 1, NULL, 0, '{}'::jsonb, 0
)
ON CONFLICT (issue_id, leader_agent_id) DO UPDATE SET
    generation = CASE
        WHEN issue_coordinator_session.runtime_id <> EXCLUDED.runtime_id
          OR (sqlc.arg(force_cold_start)::boolean AND issue_coordinator_session.session_id IS NOT NULL)
        THEN issue_coordinator_session.generation + 1
        ELSE issue_coordinator_session.generation
    END,
    session_id = CASE
        WHEN issue_coordinator_session.runtime_id <> EXCLUDED.runtime_id
          OR sqlc.arg(force_cold_start)::boolean
        THEN NULL
        ELSE issue_coordinator_session.session_id
    END,
    runtime_id = EXCLUDED.runtime_id,
    active_task_id = EXCLUDED.active_task_id,
    continuity_state = CASE
        WHEN issue_coordinator_session.runtime_id <> EXCLUDED.runtime_id
          OR sqlc.arg(force_cold_start)::boolean
        THEN 'cold_start'
        WHEN issue_coordinator_session.session_id IS NULL
        THEN 'cold_start'
        ELSE 'resumable'
    END,
    updated_at = now()
WHERE issue_coordinator_session.active_task_id IS NULL
   OR issue_coordinator_session.active_task_id = EXCLUDED.active_task_id
   OR NOT EXISTS (
       SELECT 1
       FROM agent_task_queue active_task
       WHERE active_task.id = issue_coordinator_session.active_task_id
         AND active_task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'cancel_requested')
   )
RETURNING issue_id, leader_agent_id, runtime_id, session_id, generation,
          snapshot_version, snapshot, last_event_sequence, active_task_id,
          last_task_id, continuity_state, updated_at;

-- name: CompleteIssueCoordinatorSession :one
UPDATE issue_coordinator_session
SET runtime_id = sqlc.arg(runtime_id),
    session_id = NULLIF(sqlc.arg(session_id)::text, ''),
    snapshot_version = snapshot_version + 1,
    snapshot = sqlc.arg(snapshot)::jsonb,
    last_event_sequence = last_event_sequence + 1,
    active_task_id = NULL,
    last_task_id = sqlc.arg(task_id),
    continuity_state = CASE
        WHEN NULLIF(sqlc.arg(session_id)::text, '') IS NULL THEN 'cold_start'
        ELSE 'resumable'
    END,
    updated_at = now()
WHERE issue_id = sqlc.arg(issue_id)
  AND leader_agent_id = sqlc.arg(leader_agent_id)
  AND active_task_id = sqlc.arg(task_id)
RETURNING issue_id, leader_agent_id, runtime_id, session_id, generation,
          snapshot_version, snapshot, last_event_sequence, active_task_id,
          last_task_id, continuity_state, updated_at;
