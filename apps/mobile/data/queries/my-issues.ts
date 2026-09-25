/**
 * "My Issues" list, server-filtered by scope. Mirrors the three scopes web
 * exposes in `packages/views/my-issues/components/my-issues-page.tsx:48-65`:
 *   - assigned: issues where assignee_id = me
 *   - created:  issues where creator_id  = me
 *   - agents:   issues where the assignee is an *indirect* extension of me —
 *               an owned agent, OR a squad I'm a human member of, lead, or
 *               have an owned agent inside. Driven server-side by the
 *               `involves_user_id` predicate (see MUL-2397, 2026-05-19).
 *               Direct member assignment is intentionally EXCLUDED — that's
 *               the `assigned` scope's meaning.
 *
 * The mobile-only `actionable`（待我推进）scope (RUYI-76 ①) has no relation of
 * its own — it unions the three above, each narrowed to the four action
 * categories. See `myScopeFilters` + `lib/my-actionable-issues.ts`.
 *
 * Cache key shape is `issueKeys.myList(wsId, scope, filter)` — same prefix
 * as web's `packages/core/issues/queries.ts` so a future WS handler can
 * invalidate `issueKeys.myAll(wsId)` and reach both clients.
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";
import { ACTIONABLE_CATEGORIES } from "@/lib/my-actionable-issues";
import {
  issueKeys,
  type MyIssuesFilter,
  type MyIssuesScope,
  type SingleRelationScope,
} from "./issue-keys";

export function buildMyIssuesFilter(
  scope: SingleRelationScope,
  userId: string,
): MyIssuesFilter {
  switch (scope) {
    case "assigned":
      return { assignee_id: userId };
    case "created":
      return { creator_id: userId };
    case "agents":
      return { involves_user_id: userId };
  }
}

/**
 * The three per-relation filters behind the merged `actionable`（待我推进）
 * scope (RUYI-76 ①), each narrowed to the four action categories server-side.
 *
 * The category restriction has to travel to the server, not just run in
 * `buildActionableIssues` (RUYI-199). `GET /api/issues` returns at most 100
 * rows per request and orders by manual `position` ASC, and terminal issues
 * accumulate at the head of that order — a workspace where the user's agents
 * closed a few hundred issues fills the entire first page with done/cancelled
 * rows, so the client-side filter drops all 100 and the view renders empty
 * while every actionable issue sits on page 2. Passing `status_categories`
 * makes the server's window itself actionable-only, which restores the view
 * and keeps `buildActionableIssues` as the second line of defense for an older
 * backend that ignores the parameter.
 *
 * This deliberately gives the three actionable queries their OWN cache keys
 * rather than sharing the single scopes' entries: the shapes now differ, and a
 * shared key would let the unfiltered Assigned/Created/Agents tab overwrite the
 * merged view's data with rows it must not show.
 */
export function myScopeFilters(
  userId: string,
): Record<SingleRelationScope, MyIssuesFilter> {
  const categories = [...ACTIONABLE_CATEGORIES];
  return {
    assigned: { assignee_id: userId, status_categories: categories },
    created: { creator_id: userId, status_categories: categories },
    agents: { involves_user_id: userId, status_categories: categories },
  };
}

export const myIssueListOptions = (
  wsId: string | null,
  scope: MyIssuesScope,
  filter: MyIssuesFilter,
) =>
  queryOptions({
    queryKey: issueKeys.myList(wsId, scope, filter),
    queryFn: async ({ signal }) => {
      const res = await api.listIssues(filter, { signal });
      return res.issues;
    },
    enabled: !!wsId,
  });
