-- Normalize daemon_id by stripping the trailing `.local` mDNS suffix.
--
-- Daemons started via different methods on macOS used to register with
-- inconsistent hostnames: standalone CLI got `MacBook-Air` while the
-- desktop-bundled binary got `MacBook-Air.local` (or vice versa). PR #1070
-- (commit 6428a100) fixed the daemon side by stripping `.local` at hostname
-- resolution time, but did not address existing rows.
--
-- Without this migration, every macOS user upgrading past 6428a100 will
-- have all of their `agent_runtime` rows inserted again under the new
-- canonical `daemon_id`, leaving the old rows orphaned and the agents
-- (which reference `agent_runtime.id` via FK) pointing at runtimes that
-- no longer receive heartbeats.
--
-- Strategy:
--   1. For every (workspace_id, provider) where both `X` and `X.local`
--      exist, keep `X` as the canonical row and redirect both
--      `agent.runtime_id` and `agent_task_queue.runtime_id` from the
--      `.local` row to the canonical row, then delete the duplicate.
--   2. For any remaining rows that still end in `.local` (no canonical
--      counterpart), strip the suffix in place.
--
-- Note: `TRIM(TRAILING '.local' FROM ...)` is unsafe because TRIM treats
-- its argument as a character set, not a substring; we use a substring
-- expression on the LIKE-matched rows instead.

WITH pairs AS (
    SELECT
        canonical.id AS keep_id,
        dot_local.id AS dup_id
    FROM agent_runtime canonical
    INNER JOIN agent_runtime dot_local
        ON canonical.workspace_id = dot_local.workspace_id
       AND canonical.provider = dot_local.provider
       AND dot_local.daemon_id = canonical.daemon_id || '.local'
),
agent_redirect AS (
    UPDATE agent
    SET runtime_id = pairs.keep_id
    FROM pairs
    WHERE agent.runtime_id = pairs.dup_id
    RETURNING agent.id
),
queue_redirect AS (
    UPDATE agent_task_queue
    SET runtime_id = pairs.keep_id
    FROM pairs
    WHERE agent_task_queue.runtime_id = pairs.dup_id
    RETURNING agent_task_queue.id
)
DELETE FROM agent_runtime
WHERE id IN (SELECT dup_id FROM pairs);

UPDATE agent_runtime
SET
    daemon_id = substring(daemon_id from 1 for length(daemon_id) - length('.local')),
    updated_at = now()
WHERE daemon_id LIKE '%.local';
