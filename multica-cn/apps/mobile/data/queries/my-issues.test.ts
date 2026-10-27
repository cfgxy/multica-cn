/**
 * Filters behind the My Issues scopes (RUYI-76 ①, RUYI-199).
 *
 * The regression this file guards: `GET /api/issues` caps a response at 100
 * rows ordered by manual `position` ASC, and terminal (done/cancelled) issues
 * accumulate at the head of that order. With no server-side category filter
 * the merged 待我推进 view received 100 terminal rows, dropped every one of
 * them in `buildActionableIssues`, and rendered empty while the actionable
 * issues sat on page 2.
 */
import { describe, expect, it, vi } from "vitest";

// data-layer tests must never load the native fetch chain (vitest.config.ts).
vi.mock("@/data/api", () => ({ api: {} }));

import { ACTIONABLE_CATEGORIES } from "@/lib/my-actionable-issues";
import { buildMyIssuesFilter, myScopeFilters } from "./my-issues";

const USER = "user-1";

describe("myScopeFilters", () => {
  it("keeps one relation predicate per scope", () => {
    const filters = myScopeFilters(USER);

    expect(filters.assigned.assignee_id).toBe(USER);
    expect(filters.created.creator_id).toBe(USER);
    expect(filters.agents.involves_user_id).toBe(USER);
  });

  it("narrows every relation to the four action categories server-side", () => {
    const filters = myScopeFilters(USER);

    for (const scope of ["assigned", "created", "agents"] as const) {
      expect(filters[scope].status_categories).toEqual([
        ...ACTIONABLE_CATEGORIES,
      ]);
    }
  });

  it("does not share a filter shape with the single-relation scopes", () => {
    // Cache keys are built from the filter, so an identical shape would let
    // the unfiltered Assigned/Created/Agents tab overwrite the merged view's
    // data with terminal rows it must not show.
    const merged = myScopeFilters(USER);

    expect(merged.assigned).not.toEqual(buildMyIssuesFilter("assigned", USER));
    expect(merged.created).not.toEqual(buildMyIssuesFilter("created", USER));
    expect(merged.agents).not.toEqual(buildMyIssuesFilter("agents", USER));
  });
});

describe("buildMyIssuesFilter", () => {
  it("leaves the single-relation scopes unfiltered by status", () => {
    for (const scope of ["assigned", "created", "agents"] as const) {
      expect(
        buildMyIssuesFilter(scope, USER).status_categories,
      ).toBeUndefined();
    }
  });
});
