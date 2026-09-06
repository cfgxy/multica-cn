/**
 * Per-issue / per-agent slices of the workspace agent-task snapshot.
 * Mobile-owned mirror of the pure selectors in web
 * `packages/views/issues/surface/activity.ts` (`selectIssueTasks`,
 * `isQueuedTaskStatus`) — mobile cannot import `packages/views`
 * (apps/mobile/CLAUDE.md sharing whitelist), so the semantics are restated
 * here and locked by `issue-agent-activity.test.ts`. The two sides MUST NOT
 * drift: the snapshot endpoint, the active-status set, and the running-vs-
 * queued bucketing are product parity surfaces (web inbox rows and board
 * cards render from the same shapes).
 *
 * Consumers:
 *   - inbox rows: `deriveIssueActivityMap` runs once per screen render and
 *     feeds the running/queued badge per row (RUYI-76 ③). Screen-level
 *     derivation instead of web's per-row `useQuery(select)` — one pass for
 *     the whole list, no per-row query observers on a long FlatList.
 *   - agents screens: `selectAgentActiveTasks` drives the per-agent running
 *     task list (RUYI-76 ②).
 *
 * The snapshot query itself lives in `data/queries/agent-task-snapshot.ts`
 * and is warmed at workspace entry + invalidated on task lifecycle WS events
 * by `use-presence-realtime.ts`.
 */
import type { AgentTask } from "@multica/core/types";

/** Queued-side statuses. Mirrors web `isQueuedTaskStatus` verbatim —
 *  `waiting_local_directory` is the daemon's path-lock hold state and still
 *  counts as "on the plate", not running. */
function isQueuedTaskStatus(status: AgentTask["status"]): boolean {
  return (
    status === "queued" ||
    status === "dispatched" ||
    status === "waiting_local_directory"
  );
}

/** Anything that is neither terminal nor history — the set every "is an
 *  agent working on this" consumer buckets on. */
export function isActiveTaskStatus(status: AgentTask["status"]): boolean {
  return status === "running" || isQueuedTaskStatus(status);
}

export interface IssueActivity {
  running: AgentTask[];
  queued: AgentTask[];
}

/** Per-issue slice: running + queued tasks, terminal tasks dropped. Mirrors
 *  web `selectIssueTasks` — same bucket order (running first), same treatment
 *  of chat/autopilot tasks (empty `issue_id` never matches an issue). */
export function selectIssueActivity(
  snapshot: readonly AgentTask[],
  issueId: string,
): IssueActivity {
  const running: AgentTask[] = [];
  const queued: AgentTask[] = [];
  for (const task of snapshot) {
    if (task.issue_id !== issueId) continue;
    if (task.status === "running") running.push(task);
    else if (isQueuedTaskStatus(task.status)) queued.push(task);
  }
  return { running, queued };
}

/**
 * Whole-workspace activity index for list screens: issue id → its active
 * tasks. One pass over the snapshot; issues with only terminal tasks are
 * absent (the badge renders nothing for them — same visibility rule as web,
 * where the indicator returns null).
 */
export function deriveIssueActivityMap(
  snapshot: readonly AgentTask[],
): Map<string, IssueActivity> {
  const map = new Map<string, IssueActivity>();
  for (const task of snapshot) {
    if (!task.issue_id) continue;
    if (!isActiveTaskStatus(task.status)) continue;
    let entry = map.get(task.issue_id);
    if (!entry) {
      entry = { running: [], queued: [] };
      map.set(task.issue_id, entry);
    }
    if (task.status === "running") entry.running.push(task);
    else entry.queued.push(task);
  }
  return map;
}

/** Newest-first key for display ordering: tasks that started have a
 *  `started_at`; queued-side tasks fall back to `created_at`. */
function recencyKey(task: AgentTask): string {
  return task.started_at ?? task.created_at ?? "";
}

/**
 * One agent's active tasks, running first then queued, newest first within
 * each group. Chat- and autopilot-spawned runs (empty `issue_id`) ARE the
 * agent's runs and stay in the list — the per-agent view answers "what is
 * this agent working on", not "what issues is it touching".
 */
export function selectAgentActiveTasks(
  snapshot: readonly AgentTask[],
  agentId: string,
): AgentTask[] {
  const running: AgentTask[] = [];
  const queued: AgentTask[] = [];
  for (const task of snapshot) {
    if (task.agent_id !== agentId) continue;
    if (task.status === "running") running.push(task);
    else if (isQueuedTaskStatus(task.status)) queued.push(task);
  }
  const byRecencyDesc = (a: AgentTask, b: AgentTask) =>
    recencyKey(b).localeCompare(recencyKey(a));
  running.sort(byRecencyDesc);
  queued.sort(byRecencyDesc);
  return [...running, ...queued];
}
