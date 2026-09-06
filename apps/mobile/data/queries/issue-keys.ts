/**
 * Centralised TanStack Query keys for issue-domain queries on mobile.
 *
 * Prefix shape mirrors web's `packages/core/issues/queries.ts` so the same
 * WS invalidation surface (e.g. `invalidateQueries({ queryKey: issueKeys.myAll(wsId) })`)
 * eventually drives both clients. Keys are workspace-scoped — switching
 * workspace flips wsId and the cache moves automatically (root CLAUDE.md
 * "Workspace-scoped queries must key on wsId").
 */
import type { ListIssuesParams } from "@multica/core/types";

/**
 * `actionable`（待我推进）is mobile-only (RUYI-76 ①): a client-side union of
 * the three server relations restricted to the four action categories
 * (backlog/todo/in_progress/in_review). It has NO single server filter —
 * `buildMyIssuesFilter` deliberately doesn't accept it; the screen mounts the
 * three per-relation queries (whose keys match the single scopes' exactly, so
 * cache entries are shared) and merges via `buildActionableIssues`.
 */
export type MyIssuesScope = "assigned" | "created" | "agents" | "actionable";

/** The scopes that map to one server filter each. */
export type SingleRelationScope = Exclude<MyIssuesScope, "actionable">;

export type MyIssuesFilter = Pick<
  ListIssuesParams,
  "assignee_id" | "assignee_ids" | "creator_id" | "involves_user_id"
>;

export const issueKeys = {
  all: (wsId: string | null) => ["issues", wsId] as const,
  list: (wsId: string | null) => [...issueKeys.all(wsId), "list"] as const,
  myAll: (wsId: string | null) => [...issueKeys.all(wsId), "my"] as const,
  myList: (
    wsId: string | null,
    scope: MyIssuesScope,
    filter: MyIssuesFilter,
  ) => [...issueKeys.myAll(wsId), scope, filter] as const,
  detail: (wsId: string | null, id: string) =>
    [...issueKeys.all(wsId), "detail", id] as const,
  timeline: (wsId: string | null, id: string) =>
    [...issueKeys.all(wsId), "timeline", id] as const,
  // Currently-running tasks for an issue (queued/dispatched/running). Drives
  // the "Working" state of the AgentActivityRow inside IssueHeaderCard.
  activeTasks: (wsId: string | null, id: string) =>
    [...issueKeys.all(wsId), "active-tasks", id] as const,
  // All tasks (any status) for an issue — drives the Runs history sheet.
  tasks: (wsId: string | null, id: string) =>
    [...issueKeys.all(wsId), "tasks", id] as const,
  // File attachments hooked to an issue (and its comments). Used by the
  // markdown renderer to resolve `mc://file/<id>` URIs to download_url.
  attachments: (wsId: string | null, id: string) =>
    [...issueKeys.all(wsId), "attachments", id] as const,
};
