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

/**
 * Per-task cancel affordance (RUYI-538 ③): everything isActiveTaskStatus
 * covers, plus the two-phase `cancel_requested` hold — the daemon hasn't
 * confirmed the interrupt yet and a repeat cancel re-broadcasts the nudge
 * (AgentTask.status type doc; mobile RunRow's CancelButton follows the same
 * set). Terminal rows (completed / failed / cancelled) never offer cancel.
 */
export function canCancelAgentTask(task: AgentTask): boolean {
  return isActiveTaskStatus(task.status) || task.status === "cancel_requested";
}

export interface IssueActivity {
  running: AgentTask[];
  queued: AgentTask[];
}

/** Avatar treatment bucket for the unified status language (RUYI-554):
 *  running → breathing animation, queued → grayed, absent → static. */
export type AgentActivityState = "running" | "queued";

/**
 * Per-actor slice of one issue's activity groups: does THIS actor have a
 * running task (→ "running"), else a queued-side task (→ "queued"), else
 * nothing (→ null). Feeds ActorAvatar's `activity` prop on rows that already
 * hold the issue slice, so list rows derive the avatar state with zero extra
 * queries. Member actors simply never match an AgentTask.agent_id and fall
 * out as null — the static branch.
 */
export function selectActorActivity(
  activity: IssueActivity | undefined,
  actorId: string | null | undefined,
): AgentActivityState | null {
  if (!activity || !actorId) return null;
  if (activity.running.some((task) => task.agent_id === actorId)) {
    return "running";
  }
  if (activity.queued.some((task) => task.agent_id === actorId)) {
    return "queued";
  }
  return null;
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
 * Visual bucket for row rendering (RUYI-700 交付2/3): running-side tasks
 * breathe, queued-side tasks gray. `cancel_requested` belongs to the
 * RUNNING side — the stop request is accepted but the task still holds the
 * runtime, so it reads as alive-with-a-warning, not as queued. (The old
 * row-level `=== "running"` check grayed it into the queued look — the
 * defect this helper corrects.) Parity selectors above keep their own
 * bucketing; this one is display-only.
 */
export function agentTaskActivityState(
  status: AgentTask["status"],
): AgentActivityState {
  return status === "running" || status === "cancel_requested"
    ? "running"
    : "queued";
}

export interface IssueOtherAgentsActivity {
  /** Distinct other-agent ids on the issue, all matching `activity`. When a
   *  running-side agent exists the queued-side others are omitted — the
   *  stack renders one activity for every avatar, and a grayed-in-a-breathing-
   *  stack avatar would misrepresent its task state. */
  agents: { type: "agent"; id: string }[];
  /** One bucket for the whole stack: running wins over queued — the same
   *  rule the activity badge uses (AvatarStack renders a single value). */
  activity: AgentActivityState | null;
}

export const NO_OTHER_AGENTS: IssueOtherAgentsActivity = {
  agents: [],
  activity: null,
};

/**
 * RUYI-700 行3：同一 Issue 上除本 Agent 外的其他活跃 Agent，供行尾
 * AvatarStack 渲染。Snapshot holds active tasks only, so one pass is the
 * whole story. `cancel_requested` counts as running-side here too — the
 * stop is pending but the run still occupies the runtime, which is exactly
 * what "other agents currently working on this issue" wants to show.
 */
export function selectIssueOtherAgentsActivity(
  snapshot: readonly AgentTask[],
  issueId: string,
  excludeAgentId: string | null | undefined,
): IssueOtherAgentsActivity {
  const running: string[] = [];
  const queued: string[] = [];
  for (const task of snapshot) {
    if (task.issue_id !== issueId || !task.agent_id) continue;
    if (task.agent_id === excludeAgentId) continue;
    if (task.status === "running" || task.status === "cancel_requested") {
      if (!running.includes(task.agent_id)) running.push(task.agent_id);
    } else if (isQueuedTaskStatus(task.status)) {
      if (!queued.includes(task.agent_id)) queued.push(task.agent_id);
    }
  }
  if (running.length > 0) {
    return {
      agents: running.map((id) => ({ type: "agent" as const, id })),
      activity: "running",
    };
  }
  if (queued.length > 0) {
    return {
      agents: queued.map((id) => ({ type: "agent" as const, id })),
      activity: "queued",
    };
  }
  return NO_OTHER_AGENTS;
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
