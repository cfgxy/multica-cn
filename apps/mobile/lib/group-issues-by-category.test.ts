// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Issue } from "@multica/core/types";
import { statusCategoryOfKey } from "./issue-status";
import {
  categoryOrderFromStatusFilters,
  groupIssuesByCategory,
} from "./group-issues-by-category";

function issue(id: string, status: string, statusCategory?: string): Issue {
  return {
    id,
    workspace_id: "ws-1",
    number: 1,
    identifier: `MUL-${id}`,
    title: id,
    description: null,
    status,
    ...(statusCategory ? { status_category: statusCategory as Issue["status_category"] } : {}),
    priority: "none",
    assignee_type: null,
    assignee_id: null,
    creator_type: "member",
    creator_id: "user-1",
    parent_issue_id: null,
    project_id: null,
    position: 0,
    stage: null,
    start_date: null,
    due_date: null,
    metadata: {},
    properties: {},
    created_at: "",
    updated_at: "",
  };
}

describe("groupIssuesByCategory", () => {
  // The regression this exists for: bucketing by `issue.status` created a `qa`
  // bucket no section ever read, so the issue was simply not on the screen
  // while the header counts said nothing was wrong (MUL-6457).
  it("puts a custom status in its category's section", () => {
    const sections = groupIssuesByCategory([
      issue("a", "qa", "in_review"),
      issue("b", "in_review"),
    ]);
    expect(sections).toHaveLength(1);
    expect(sections[0].category).toBe("in_review");
    expect(sections[0].data.map((i) => i.id)).toEqual(["a", "b"]);
  });

  it("orders sections canonically and drops empty ones", () => {
    const sections = groupIssuesByCategory([
      issue("a", "done"),
      issue("b", "backlog"),
      issue("c", "in_progress"),
    ]);
    expect(sections.map((s) => s.category)).toEqual(["backlog", "in_progress", "done"]);
  });

  // A workspace with no custom statuses must behave exactly as it did before
  // the catalog existed — one section per built-in that has rows.
  it("groups built-in statuses one section each", () => {
    const sections = groupIssuesByCategory([
      issue("a", "todo"),
      issue("b", "todo"),
      issue("c", "blocked"),
    ]);
    expect(sections.map((s) => [s.category, s.data.length])).toEqual([
      ["todo", 2],
      ["blocked", 1],
    ]);
  });

  // Cancelled has no section on mobile. A custom status in that category is
  // hidden here for the same reason the built-in is: it inherits the category's
  // behavior.
  it("omits the cancelled category, built-in or custom", () => {
    expect(groupIssuesByCategory([issue("a", "cancelled")])).toEqual([]);
    expect(groupIssuesByCategory([issue("a", "wont_do", "cancelled")])).toEqual([]);
  });

  // Older backends predate `status_category`; a custom key then resolves to
  // nothing. Landing it in `todo` keeps the row on screen, which beats a row in
  // no section at all.
  it("still shows a custom status the payload could not resolve", () => {
    const sections = groupIssuesByCategory([issue("a", "qa")]);
    expect(sections.map((s) => s.category)).toEqual(["todo"]);
    expect(sections[0].data.map((i) => i.id)).toEqual(["a"]);
  });

  it("returns nothing for an empty list", () => {
    expect(groupIssuesByCategory([])).toEqual([]);
  });
});

// RUYI-344: the full-space Tasks tab's "全部" view is the one surface that
// must also show cancelled issues — as its own section, pinned last, and
// self-hiding when empty. The default call keeps the cancelled-less shape
// every other list relies on.
describe("groupIssuesByCategory({ includeCancelled })", () => {
  it("appends a cancelled section after the canonical ones", () => {
    const sections = groupIssuesByCategory(
      [
        issue("a", "cancelled"),
        issue("b", "done"),
        issue("c", "backlog"),
        issue("d", "blocked"),
      ],
      { includeCancelled: true },
    );
    expect(sections.map((s) => s.category)).toEqual([
      "backlog",
      "done",
      "blocked",
      "cancelled",
    ]);
    expect(sections[3].data.map((i) => i.id)).toEqual(["a"]);
  });

  it("keeps a custom cancelled-category status in that section", () => {
    const sections = groupIssuesByCategory(
      [issue("a", "wont_do", "cancelled")],
      { includeCancelled: true },
    );
    expect(sections).toHaveLength(1);
    expect(sections[0].category).toBe("cancelled");
  });

  it("hides the cancelled section when it has no rows", () => {
    const sections = groupIssuesByCategory([issue("a", "todo")], {
      includeCancelled: true,
    });
    expect(sections.map((s) => s.category)).toEqual(["todo"]);
  });

  it("leaves the default call untouched (no cancelled section)", () => {
    expect(
      groupIssuesByCategory([issue("a", "cancelled")], {}),
    ).toEqual([]);
  });
});

// RUYI-344 增量：全部 TAB 的分组顺序跟随状态多选的勾选顺序。勾选顺序以
// 状态 KEY 记录（statusFilters 追加式），分组以 CATEGORY 为单元——
// categoryOrderFromStatusFilters 把 key 顺序解析成 category 顺序，
// groupIssuesByCategory 的 categoryOrder 选项负责按它装配 sections。
describe("categoryOrderFromStatusFilters", () => {
  it("maps built-in keys to their own categories in check order", () => {
    expect(
      categoryOrderFromStatusFilters(["blocked", "in_progress", "backlog"]),
    ).toEqual(["blocked", "in_progress", "backlog"]);
  });

  it("resolves custom status keys through the injected resolver", () => {
    const catalogCategoryOf = (key: string) =>
      key === "in-dev"
        ? "in_progress"
        : key === "wont-do"
          ? "cancelled"
          : statusCategoryOfKey(key);
    expect(
      categoryOrderFromStatusFilters(
        ["in-dev", "wont-do", "blocked"],
        catalogCategoryOf,
      ),
    ).toEqual(["in_progress", "cancelled", "blocked"]);
  });

  it("dedupes categories, keeping the first position", () => {
    const twoCustomsOneCategory = (key: string) =>
      key === "dev-a" || key === "dev-b" ? "in_progress" : "todo";
    expect(
      categoryOrderFromStatusFilters(["dev-a", "todo", "dev-b"], twoCustomsOneCategory),
    ).toEqual(["in_progress", "todo"]);
  });

  it("falls back to todo for keys nothing resolves", () => {
    expect(categoryOrderFromStatusFilters(["mystery", "qa"])).toEqual(["todo"]);
  });

  it("returns nothing for an empty selection", () => {
    expect(categoryOrderFromStatusFilters([])).toEqual([]);
  });
});

describe("groupIssuesByCategory({ categoryOrder })", () => {
  it("orders sections by the given order, remainder canonically behind", () => {
    const sections = groupIssuesByCategory(
      [issue("a", "done"), issue("b", "backlog"), issue("c", "blocked")],
      { categoryOrder: ["blocked", "in_progress", "backlog"] },
    );
    expect(sections.map((s) => s.category)).toEqual([
      "blocked",
      "backlog",
      "done",
    ]);
  });

  it("keeps cancelled pinned last when the order does not mention it", () => {
    const sections = groupIssuesByCategory(
      [issue("a", "cancelled"), issue("b", "done")],
      { includeCancelled: true, categoryOrder: ["done"] },
    );
    expect(sections.map((s) => s.category)).toEqual(["done", "cancelled"]);
  });

  it("places a selected cancelled category at its selection position", () => {
    const sections = groupIssuesByCategory(
      [issue("a", "cancelled"), issue("b", "blocked")],
      { includeCancelled: true, categoryOrder: ["cancelled", "blocked"] },
    );
    expect(sections.map((s) => s.category)).toEqual(["cancelled", "blocked"]);
  });

  it("ignores a cancelled entry when includeCancelled is off", () => {
    const sections = groupIssuesByCategory(
      [issue("a", "todo"), issue("b", "cancelled")],
      { categoryOrder: ["cancelled", "todo"] },
    );
    expect(sections.map((s) => s.category)).toEqual(["todo"]);
  });

  it("keeps the canonical order for an empty order array", () => {
    const sections = groupIssuesByCategory(
      [issue("a", "done"), issue("b", "backlog")],
      { categoryOrder: [] },
    );
    expect(sections.map((s) => s.category)).toEqual(["backlog", "done"]);
  });
});
