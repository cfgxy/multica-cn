/**
 * Full-space Tasks tab queries (RUYI-344) — the workspace-wide list behind
 * the bottom "任务" tab, sliced into five quadrant TABs.
 *
 * Wire contract (pinned by tasks.test.ts):
 *   - Every TAB narrows server-side through `status_categories` ("全部"
 *     sends none). Category, not status key, so custom statuses inherit
 *     their category's TAB and section (MUL-6457 rule).
 *   - Sort always travels (`sort_by` + `sort_direction`), default
 *     updated_at desc. No ascending in v1 — mobile simplification. Sending
 *     the sort also tilts the server's 100-row window toward recently
 *     active issues (the RUYI-199 window concern).
 *   - Status multi-select and priority filters stay OFF the wire — they
 *     filter the loaded window client-side via the shared `filterIssues`
 *     (same-N parity with the pre-refactor 全部 list and the my-issues
 *     screen).
 *   - Mine relations map to the three server predicates
 *     (`assignee_id` / `creator_id` / `involves_user_id`). One checked →
 *     one query; ≥2 checked → three queries + `mergeTaskIssues` union
 *     (the old actionable pattern, generalized — no category restriction).
 *   - Actor picker selections travel as `assignee_filters` +
 *     `include_no_assignee` / `creator_filters` — the same OR-group the
 *     server builds for web's actor facets (issue.go:1374).
 *   - `agentRunning` restricts the window to the live running-issue set
 *     derived from the agent-task-snapshot — the mobile counterpart of
 *     web's working-agents projection feeding `working_issue_ids`/`ids`.
 *     Neither client has this marker on the Issue payload itself; both
 *     derive it from a task snapshot.
 *
 * Cache keys sit INSIDE the `list(wsId)` prefix (`issueKeys.taskList`), so
 * prefix invalidation from mutations/WS-reconnect reaches them unchanged
 * and the WS patchers cover the whole family with `setQueriesData`.
 */
import { queryOptions } from "@tanstack/react-query";
import type {
  AgentTask,
  Issue,
  IssueStatusCategory,
  ListIssuesParams,
} from "@multica/core/types";
import { api } from "@/data/api";
import type { MineRelation, TaskSortKey, TaskTab } from "@/data/stores/tasks-view-store";
import { issueKeys } from "./issue-keys";

/** Per-TAB server window. `all` sends no parameter. */
export const TASK_TAB_CATEGORIES: Record<TaskTab, IssueStatusCategory[] | undefined> = {
  all: undefined,
  open: ["backlog", "todo"],
  active: ["in_progress", "in_review"],
  blocked: ["blocked"],
  completed: ["done"],
};

export interface TaskListFilterInput {
  tab: TaskTab;
  sortBy: TaskSortKey;
  userId: string | null;
  /** Which single mine relation to apply (undefined = none). */
  mine?: MineRelation;
  assigneeRefs?: { type: "member" | "agent" | "squad"; id: string }[];
  includeNoAssignee?: boolean;
  creatorRefs?: { type: "member" | "agent" | "squad"; id: string }[];
  /** Live running-issue set; empty/undefined = no restriction. */
  runningIssueIds?: string[];
}

/**
 * Assemble the wire filter for one Tasks-tab query. Status multi-select and
 * priority are deliberately NOT inputs — they never reach the wire.
 */
export function buildTaskListFilter(input: TaskListFilterInput): ListIssuesParams {
  const filter: ListIssuesParams = {
    sort_by: input.sortBy,
    sort_direction: "desc",
  };
  const categories = TASK_TAB_CATEGORIES[input.tab];
  if (categories) filter.status_categories = categories;
  if (input.mine && input.userId) {
    if (input.mine === "assigned") filter.assignee_id = input.userId;
    else if (input.mine === "created") filter.creator_id = input.userId;
    else filter.involves_user_id = input.userId;
  }
  if (input.assigneeRefs?.length) filter.assignee_filters = [...input.assigneeRefs];
  if (input.includeNoAssignee) filter.include_no_assignee = true;
  if (input.creatorRefs?.length) filter.creator_filters = [...input.creatorRefs];
  if (input.runningIssueIds?.length) filter.ids = [...input.runningIssueIds];
  return filter;
}

export const taskListOptions = (wsId: string | null, filter: ListIssuesParams) =>
  queryOptions({
    queryKey: issueKeys.taskList(wsId, filter),
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const res = await api.listIssues(filter, { signal });
      return res.issues;
    },
    enabled: !!wsId,
  });

export interface TaskRelationSources {
  assigned: Issue[] | undefined;
  created: Issue[] | undefined;
  involved: Issue[] | undefined;
}

/**
 * Union the three mine-relation lists for the ≥2-checked case: one row per
 * issue, re-sorted by the active sort key descending. The source queries all
 * carry the same server sort, but interleaving three lists breaks the global
 * order, so the union re-establishes it client-side.
 */
export function mergeTaskIssues(
  sources: TaskRelationSources,
  sortBy: TaskSortKey,
): Issue[] {
  const seen = new Set<string>();
  const merged: Issue[] = [];
  for (const list of [sources.assigned, sources.created, sources.involved]) {
    for (const issue of list ?? []) {
      if (seen.has(issue.id)) continue;
      seen.add(issue.id);
      merged.push(issue);
    }
  }
  const key = sortBy === "created_at" ? "created_at" : "updated_at";
  return merged.sort(
    (a, b) => (b[key] ?? "").localeCompare(a[key] ?? ""),
  );
}

/**
 * Task statuses that count as "an agent is working on this" — the same
 * active set the workspace agent-task-snapshot serves (queued / dispatched /
 * waiting_local_directory / running; completed/failed/cancelled excluded,
 * matching the server's snapshot comment in handler/agent.go:2863).
 */
export const RUNNING_TASK_STATUSES = [
  "queued",
  "dispatched",
  "waiting_local_directory",
  "running",
] as const;

/**
 * Workspace-wide running-issue set from the agent task snapshot — the data
 * source behind the 智能体执行中 window restriction. Deduped, issue-less
 * tasks skipped. Returns wire-ready ids (empty = nothing running).
 */
export function runningIssueIdsFromSnapshot(
  tasks: Pick<AgentTask, "id" | "status" | "issue_id">[],
): string[] {
  const seen = new Set<string>();
  const ids: string[] = [];
  for (const task of tasks) {
    if (!task.issue_id) continue;
    if (!(RUNNING_TASK_STATUSES as readonly string[]).includes(task.status)) continue;
    if (seen.has(task.issue_id)) continue;
    seen.add(task.issue_id);
    ids.push(task.issue_id);
  }
  return ids;
}
