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
 * three per-relation queries from `myScopeFilters` and merges via
 * `buildActionableIssues`. Those filters carry `status_categories`, so their
 * keys are distinct from the single scopes' — see `myScopeFilters` for why the
 * caches must not be shared (RUYI-199).
 */
export type MyIssuesScope = "assigned" | "created" | "agents" | "actionable";

/** The scopes that map to one server filter each. */
export type SingleRelationScope = Exclude<MyIssuesScope, "actionable">;

export type MyIssuesFilter = Pick<
  ListIssuesParams,
  | "assignee_id"
  | "assignee_ids"
  | "creator_id"
  | "involves_user_id"
  // `actionable` narrows its three relation queries to the four action
  // categories server-side. Without it the server's 100-row window is filled
  // by done/cancelled rows the merge would have dropped anyway, and the view
  // renders empty while matching issues sit on page 2 (RUYI-199).
  | "status_categories"
>;

export const issueKeys = {
  all: (wsId: string | null) => ["issues", wsId] as const,
  list: (wsId: string | null) => [...issueKeys.all(wsId), "list"] as const,
  /**
   * Full-space Tasks tab (RUYI-344). The filter object sits INSIDE the
   * `list(wsId)` prefix (unlike `myList`, which uses its own "my" branch) so
   * every existing prefix invalidation on `list(wsId)` — mutations, WS
   * reconnect — reaches the parametrized entries unchanged, and the WS
   * patchers can cover them with one `setQueriesData` over the prefix.
   */
  taskList: (wsId: string | null, filter: ListIssuesParams) =>
    [...issueKeys.list(wsId), filter] as const,
  myAll: (wsId: string | null) => [...issueKeys.all(wsId), "my"] as const,
  myList: (
    wsId: string | null,
    scope: MyIssuesScope,
    filter: MyIssuesFilter,
  ) => [...issueKeys.myAll(wsId), scope, filter] as const,
  detail: (wsId: string | null, id: string) =>
    [...issueKeys.all(wsId), "detail", id] as const,
  // Bare identifier ("MUL-123") → issue point lookup. Separate branch from
  // detail so a UUID and its identifier don't share a cache entry — the
  // identifier form can 404 (wrong workspace prefix) and cache as null.
  identifier: (wsId: string | null, identifier: string) =>
    [...issueKeys.all(wsId), "identifier", identifier] as const,
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
