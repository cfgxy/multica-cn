-- name: GetIssueCoordinatorSession :one
SELECT issue_id, leader_agent_id, runtime_id, session_id, generation,
       snapshot_version, snapshot, last_event_sequence, active_task_id,
       last_task_id, continuity_state, updated_at
FROM issue_coordinator_session
WHERE issue_id = sqlc.arg(issue_id)
  AND leader_agent_id = sqlc.arg(leader_agent_id);

-- name: ListIssueCoordinatorDecisionRefs :many
SELECT id, status, answer_comment_id
FROM issue_decisions
WHERE issue_id = sqlc.arg(issue_id)
  AND workspace_id = sqlc.arg(workspace_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ClaimIssueCoordinatorSession :one
INSERT INTO issue_coordinator_session (
    issue_id, leader_agent_id, runtime_id, active_task_id, continuity_state,
    generation, session_id, snapshot_version, snapshot, last_event_sequence,
    claim_generation, claim_snapshot_version, active_issue_reads,
    active_comment_reads
)
VALUES (
    sqlc.arg(issue_id), sqlc.arg(leader_agent_id), sqlc.arg(runtime_id),
    sqlc.arg(task_id), 'fresh', 1, NULL, 0, '{}'::jsonb, 0, 1, 0, 0, 0
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
    claim_generation = CASE
        WHEN issue_coordinator_session.runtime_id <> EXCLUDED.runtime_id
          OR (sqlc.arg(force_cold_start)::boolean AND issue_coordinator_session.session_id IS NOT NULL)
        THEN issue_coordinator_session.generation + 1
        ELSE issue_coordinator_session.generation
    END,
    claim_snapshot_version = issue_coordinator_session.snapshot_version,
    active_issue_reads = CASE
        WHEN issue_coordinator_session.active_task_id = EXCLUDED.active_task_id
        THEN issue_coordinator_session.active_issue_reads
        ELSE 0
    END,
    active_comment_reads = CASE
        WHEN issue_coordinator_session.active_task_id = EXCLUDED.active_task_id
        THEN issue_coordinator_session.active_comment_reads
        ELSE 0
    END,
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
    read_observations = (
        SELECT COALESCE(jsonb_agg(observation ORDER BY ordinal), '[]'::jsonb)
        FROM (
            SELECT observation, ordinal
            FROM (
                SELECT observation, ordinal + 1 AS ordinal
                FROM jsonb_array_elements(read_observations) WITH ORDINALITY AS previous(observation, ordinal)
                UNION ALL
                SELECT jsonb_build_object(
                    'task_id', sqlc.arg(task_id)::text,
                    'issue_reads', active_issue_reads,
                    'comment_reads', active_comment_reads,
                    'duplicate_issue_reads', GREATEST(active_issue_reads - 1, 0),
                    'duplicate_comment_reads', GREATEST(active_comment_reads - 1, 0)
                ), 0
            ) all_observations
            ORDER BY ordinal
            LIMIT 30
        ) observations
    ),
    last_task_id = sqlc.arg(task_id),
    continuity_state = CASE
        WHEN NULLIF(sqlc.arg(session_id)::text, '') IS NULL THEN 'cold_start'
        ELSE 'resumable'
    END,
    updated_at = now()
WHERE issue_id = sqlc.arg(issue_id)
  AND leader_agent_id = sqlc.arg(leader_agent_id)
  AND active_task_id = sqlc.arg(task_id)
  AND generation = claim_generation
  AND snapshot_version = claim_snapshot_version
RETURNING issue_id, leader_agent_id, runtime_id, session_id, generation,
          snapshot_version, snapshot, last_event_sequence, active_task_id,
          last_task_id, continuity_state, updated_at;

-- Count successful task-token GETs to the Issue detail and comment-list
-- endpoints for the active coordinator task. Completion stores the per-run
-- totals plus duplicate counts (max(reads - 1, 0)) in read_observations.
-- name: RecordIssueCoordinatorRead :execrows
UPDATE issue_coordinator_session
SET active_issue_reads = active_issue_reads + CASE WHEN sqlc.arg(read_kind)::text = 'issue' THEN 1 ELSE 0 END,
    active_comment_reads = active_comment_reads + CASE WHEN sqlc.arg(read_kind)::text = 'comment' THEN 1 ELSE 0 END
WHERE issue_id = sqlc.arg(issue_id)
  AND leader_agent_id = sqlc.arg(leader_agent_id)
  AND active_task_id = sqlc.arg(task_id)
  AND sqlc.arg(read_kind)::text IN ('issue', 'comment');

-- name: GetIssueCoordinatorReadObservations :one
SELECT read_observations
FROM issue_coordinator_session
WHERE issue_id = sqlc.arg(issue_id)
  AND leader_agent_id = sqlc.arg(leader_agent_id);
