-- RUYI-859 stage A: management surface for the agent-level error-rate
-- backpressure policy and its suppression-event history. The tables live in
-- migration 982 (the "recent events" index in 983); the automatic
-- suppression loop that writes agent_suppression_event is stage B and is
-- not wired by these reads.

-- name: GetAgentBackpressurePolicy :one
-- The one policy row for this agent; a miss means the built-in defaults
-- (read-only monitoring, automatic suppression off) in the API layer.
SELECT * FROM agent_backpressure_policy
WHERE workspace_id = @workspace_id AND agent_id = @agent_id;

-- name: UpsertAgentBackpressurePolicy :one
-- Save or replace the agent's policy card. ON CONFLICT on the composite
-- primary key makes a second row for the same (workspace, agent) pair
-- impossible; the conflict arm replaces every field, so a save is a
-- replace, never a partial patch. updated_at is bumped on both arms so an
-- editor can tell a fresh row from a re-save.
INSERT INTO agent_backpressure_policy (
    workspace_id, agent_id, window_minutes, error_rate_threshold_pct,
    min_samples, recovery_window_minutes, suppress_on_error_rate,
    count_model_api_failures, count_execution_failures, updated_by, updated_at
) VALUES (
    @workspace_id, @agent_id, @window_minutes, @error_rate_threshold_pct,
    @min_samples, @recovery_window_minutes, @suppress_on_error_rate,
    @count_model_api_failures, @count_execution_failures, @updated_by, now()
)
ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
    window_minutes = EXCLUDED.window_minutes,
    error_rate_threshold_pct = EXCLUDED.error_rate_threshold_pct,
    min_samples = EXCLUDED.min_samples,
    recovery_window_minutes = EXCLUDED.recovery_window_minutes,
    suppress_on_error_rate = EXCLUDED.suppress_on_error_rate,
    count_model_api_failures = EXCLUDED.count_model_api_failures,
    count_execution_failures = EXCLUDED.count_execution_failures,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: DeleteAgentBackpressurePolicy :exec
-- Drop the agent's explicit policy; the API reads the built-in defaults
-- again. No row is ever auto-created by a read, so this cannot delete
-- anything the visibility card does not show.
DELETE FROM agent_backpressure_policy
WHERE workspace_id = @workspace_id AND agent_id = @agent_id;

-- name: CountAgentTerminalTasksByReasonInWindow :many
-- Per-reason terminal-task counts for one agent over a trailing window —
-- the numerator/denominator feed for the error-rate card in stage A and
-- for the entry/recovery judgments in stage B.
--
-- Terminal only, by construction: the WHERE clause selects exactly
-- `status IN ('completed', 'failed') AND completed_at IS NOT NULL`. A
-- row sitting in the queue — including a `status = 'queued'` task held
-- by the runtime backpressure gate (the hold is the runtime's stored
-- backpressure report surfaced as the task's queued_reason on the API,
-- never a task-queue column) — has no completed_at and is outside this
-- set, so queue depth can never inflate the denominator or a held task
-- ever count as a failure sample. The queued depth the card shows
-- alongside comes from CountQueuedTasksForAgent instead.
--
-- The succeeded bucket (`reason = ''`) is completed tasks: the denominator
-- is succeeded + failed combined, matching the ListDashboardFailuresDaily
-- convention. A failed row with a NULL/empty failure_reason collapses
-- into 'unclassified' so it stays countable; the Go side folds both into
-- the taskfailure display classes.
SELECT
    CASE
        WHEN atq.status = 'failed'
            THEN COALESCE(NULLIF(atq.failure_reason, ''), 'unclassified')
        ELSE ''
    END AS reason,
    count(*)::int AS task_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = @workspace_id
  AND atq.agent_id = @agent_id
  AND atq.status IN ('completed', 'failed')
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at >= now() - make_interval(mins => @window_minutes::integer)
GROUP BY 1;

-- name: ListAgentSuppressionEvents :many
-- Newest-first transition history for one agent, capped by @max_events for
-- the detail-page card (served by idx_agent_suppression_event_agent,
-- migration 983).
SELECT * FROM agent_suppression_event
WHERE workspace_id = @workspace_id AND agent_id = @agent_id
ORDER BY occurred_at DESC
LIMIT @max_events;

-- name: InsertAgentSuppressionEvent :one
-- Stage B's write path for an entered/cleared transition, with the
-- effective policy card snapshotted so history stays interpretable after
-- the config changes. Stage A only calls the reads above.
INSERT INTO agent_suppression_event (
    workspace_id, agent_id, event_type, sample_count, error_rate_pct,
    effective_policy
) VALUES (
    @workspace_id, @agent_id, @event_type, @sample_count, @error_rate_pct,
    @effective_policy
)
RETURNING *;
