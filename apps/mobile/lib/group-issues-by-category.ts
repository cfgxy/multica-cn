/**
 * Groups issues into the sections mobile's grouped lists render (MUL-6457).
 *
 * Extracted from `(tabs)/my-issues.tsx` + `more/issues.tsx` — which held the
 * same fifteen lines twice, and the same bug twice — so the rule is stated
 * once and testable without mounting a screen (same reason as
 * `lib/search-rows.ts`).
 *
 * Sections are status CATEGORIES, not status keys. A workspace's custom
 * statuses live inside their category's section rather than adding one of their
 * own, so bucketing by `issue.status` left every custom-status bucket unread
 * and its issues invisible — the "counts and visibility must agree" rule in
 * apps/mobile/CLAUDE.md.
 *
 * An active status filter needs no special handling: callers filter the rows by
 * KEY first, so a bucket can only be non-empty if one of those rows landed in
 * it. Intersecting the section order with the filter keys directly would be
 * wrong now that the two live in different spaces.
 */
import type { Issue, IssueStatusCategory } from "@multica/core/types";
import { BOARD_CATEGORIES, issueColumnCategory } from "./issue-status";

export interface IssueSection {
  category: IssueStatusCategory;
  data: Issue[];
}

export interface GroupIssuesOptions {
  /**
   * Append a `cancelled` section AFTER the canonical ones (RUYI-344). Only
   * the full-space Tasks tab's "全部" view sets this — cancelled issues are
   * excluded from every other mobile list, and the section self-hides when
   * empty (the `.filter` below runs over the combined map). A custom status
   * in the cancelled category lands there like any built-in.
   */
  includeCancelled?: boolean;
}

/**
 * Non-empty sections in canonical category order. `cancelled` has no section on
 * mobile, so an issue in that category is omitted here exactly as the built-in
 * Cancelled always was — a custom status inherits its category's behavior.
 */
export function groupIssuesByCategory(
  issues: Issue[],
  options: GroupIssuesOptions = {},
): IssueSection[] {
  if (issues.length === 0) return [];
  const byCategory = new Map<IssueStatusCategory, Issue[]>();
  for (const issue of issues) {
    const category = issueColumnCategory(issue);
    const list = byCategory.get(category);
    if (list) list.push(issue);
    else byCategory.set(category, [issue]);
  }
  const categories = options.includeCancelled
    ? [...BOARD_CATEGORIES, "cancelled" as const]
    : BOARD_CATEGORIES;
  return categories.map((category) => ({
    category,
    data: byCategory.get(category) ?? [],
  })).filter((section) => section.data.length > 0);
}
