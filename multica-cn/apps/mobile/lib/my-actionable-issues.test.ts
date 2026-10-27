/**
 * Tests for the "待我推进 / Actionable" merged My Issues scope (RUYI-76 ①).
 *
 * The view is a client-side union of the three server-filtered relations the
 * tab already exposes (assigned / created / agents→involves_user_id),
 * restricted to the four action categories the owner enumerated:
 * 待规划(backlog) / 待办(todo) / 进行中(in_progress) / 待审核(in_review).
 */
import { describe, expect, it } from "vitest";
import type { Issue } from "@multica/core/types";
import {
  ACTIONABLE_CATEGORIES,
  buildActionableIssues,
} from "./my-actionable-issues";

let seq = 0;
function issue(overrides: Partial<Issue> = {}): Issue {
  seq += 1;
  return {
    id: `issue-${seq}`,
    status: "todo",
    status_category: "todo",
    position: seq,
    ...overrides,
  } as Issue;
}

describe("ACTIONABLE_CATEGORIES", () => {
  it("is exactly the four action categories the owner enumerated", () => {
    expect(ACTIONABLE_CATEGORIES).toEqual([
      "backlog",
      "todo",
      "in_progress",
      "in_review",
    ]);
  });
});

describe("buildActionableIssues", () => {
  it("unions all three relation lists", () => {
    const assigned = issue();
    const created = issue();
    const involved = issue();

    const out = buildActionableIssues({
      assigned: [assigned],
      created: [created],
      involved: [involved],
    });

    expect(out).toHaveLength(3);
  });

  it("dedupes an issue that appears in more than one relation (first list wins)", () => {
    const both = issue();
    const involvedOnly = issue();

    const out = buildActionableIssues({
      assigned: [both],
      created: [both],
      involved: [involvedOnly],
    });

    expect(out).toHaveLength(2);
    expect(out).toContain(both);
  });

  it("keeps only backlog/todo/in_progress/in_review issues", () => {
    const backlog = issue({ status: "backlog", status_category: "backlog" });
    const todo = issue({ status: "todo", status_category: "todo" });
    const inProgress = issue({
      status: "in_progress",
      status_category: "in_progress",
    });
    const inReview = issue({
      status: "in_review",
      status_category: "in_review",
    });
    const done = issue({ status: "done", status_category: "done" });
    const blocked = issue({ status: "blocked", status_category: "blocked" });
    const cancelled = issue({
      status: "cancelled",
      status_category: "cancelled",
    });

    const out = buildActionableIssues({
      assigned: [done, blocked, cancelled],
      created: [backlog, todo, inProgress, inReview],
      involved: [],
    });

    expect(out).toEqual([backlog, todo, inProgress, inReview]);
  });

  it("buckets by CATEGORY, not status key — a custom status in the in_review category stays", () => {
    const customReview = issue({
      status: "human-review",
      status_category: "in_review",
    });
    const customDone = issue({
      status: "shipped",
      status_category: "done",
    });

    const out = buildActionableIssues({
      assigned: [customReview, customDone],
      created: [],
      involved: [],
    });

    expect(out).toEqual([customReview]);
  });

  it("sorts by the server's manual position (asc) so ordering matches the single scopes", () => {
    const p3 = issue({ position: 3 });
    const p1 = issue({ position: 1 });
    const p2 = issue({ position: 2 });

    const out = buildActionableIssues({
      assigned: [p3, p1],
      created: [p2],
      involved: [],
    });

    expect(out.map((i) => i.position)).toEqual([1, 2, 3]);
  });

  it("returns an empty list when every source is empty", () => {
    expect(
      buildActionableIssues({ assigned: [], created: [], involved: [] }),
    ).toEqual([]);
  });
});
