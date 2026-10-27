/**
 * "待我推进 / Actionable" merged My Issues scope (RUYI-76 ①).
 *
 * Why this exists: the `assigned` scope filters `i.assignee_id = <user uuid>`
 * server-side (server/internal/handler/issue.go). In squad-driven workspaces
 * issues are assigned to the SQUAD — `assignee_id` carries the squad's UUID,
 * never a human member's — so the personal assigned list is legitimately
 * empty most of the time. `involves_user_id` (the agents scope) widens to
 * owned agents + squads but deliberately excludes direct member assignment
 * to stay disjoint from tab 1 (server issue.go:1104-1110). Neither answers
 * "what is waiting on me across statuses".
 *
 * The view is the client-side union of the three relations the tab already
 * exposes — assigned ∪ created ∪ involved — restricted to the four action
 * categories the owner enumerated (待规划/待办/进行中/待审核). No server
 * change: each source list is the exact same server query + cache entry the
 * single scopes use, so counts stay consistent with those tabs by
 * construction (same filter → same N, apps/mobile/CLAUDE.md parity rule).
 *
 * Category restriction (not status-key): a custom status in the `in_review`
 * category behaves as In Review and stays — same MUL-6457 rule the section
 * grouping follows. `blocked` / `done` / `cancelled` are out of scope: the
 * owner enumerated four categories; blocked work is visible in the other
 * scopes and its "who unblocks it" ownership is a different question.
 */
import type { Issue, IssueStatusCategory } from "@multica/core/types";
import { issueColumnCategory } from "./issue-status";

/** 待规划 / 待办 / 进行中 / 待审核 — the categories whose issues wait on a
 *  human action. Everything else (done / blocked / cancelled) is terminal or
 *  parked outside this view by definition. */
export const ACTIONABLE_CATEGORIES: readonly IssueStatusCategory[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
];

export interface ActionableSources {
  /** `assignee_id = me` — server filter of the assigned scope. */
  assigned: Issue[];
  /** `creator_id = me` — server filter of the created scope. */
  created: Issue[];
  /** `involves_user_id = me` — server filter of the agents scope. */
  involved: Issue[];
}

/**
 * Union the three relation lists, keep one row per issue (an issue assigned
 * to me that I also created appears once), drop every non-action category,
 * and order by the server's manual `position` — the same default sort the
 * single-scope lists render in, so the merged view never re-orders rows the
 * user just saw one tab over.
 */
export function buildActionableIssues(sources: ActionableSources): Issue[] {
  const seen = new Set<string>();
  const merged: Issue[] = [];
  for (const list of [sources.assigned, sources.created, sources.involved]) {
    for (const issue of list) {
      if (seen.has(issue.id)) continue;
      seen.add(issue.id);
      if (!ACTIONABLE_CATEGORIES.includes(issueColumnCategory(issue))) continue;
      merged.push(issue);
    }
  }
  return merged.sort((a, b) => a.position - b.position);
}
