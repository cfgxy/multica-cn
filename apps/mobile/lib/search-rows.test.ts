import { describe, it, expect, vi } from "vitest";
import type {
  Issue,
  SearchIssueResult,
  SearchProjectResult,
} from "@multica/core/types";

/**
 * 分组标题接的是 `search:groups.*`。这里让 mock 直接回显 key，断言里的
 * `#search:groups.recent` 就同时锁住了两件事：标题真的经过 i18n，且用的
 * 是哪个 key —— 若换成英文字面量或接错 key，形状断言立刻报红。
 */
vi.mock("i18next", () => ({ default: { t: (key: string) => key } }));

import { buildSearchRows } from "./search-rows";

function issue(
  partial: Partial<SearchIssueResult> & { id: string },
): SearchIssueResult {
  return {
    id: partial.id,
    number: partial.number ?? 1,
    identifier: partial.identifier ?? `MUL-${partial.number ?? 1}`,
    title: partial.title ?? "Untitled",
    status: partial.status ?? "todo",
    match_source: partial.match_source ?? "title",
  } as SearchIssueResult;
}

function project(
  partial: Partial<SearchProjectResult> & { id: string },
): SearchProjectResult {
  return {
    id: partial.id,
    title: partial.title ?? "Untitled",
    status: partial.status ?? "in_progress",
    match_source: partial.match_source ?? "title",
  } as SearchProjectResult;
}

/** Header titles and row keys, top to bottom — what the user actually sees. */
const shape = (rows: ReturnType<typeof buildSearchRows>) =>
  rows.map((r) => (r.kind === "header" ? `#${r.title}` : r.key));

describe("buildSearchRows", () => {
  it("renders Recent for an empty query", () => {
    const rows = buildSearchRows({
      query: "  ",
      issues: [],
      projects: [],
      recentIssues: [{ id: "r1" } as Issue, { id: "r2" } as Issue],
    });
    expect(shape(rows)).toEqual(["#search:groups.recent", "r-r1", "r-r2"]);
  });

  it("returns nothing when there is neither a query nor recent history", () => {
    expect(
      buildSearchRows({ query: "", issues: [], projects: [], recentIssues: [] }),
    ).toEqual([]);
  });

  // MUL-5824 regression: this screen renders every project before every issue,
  // so a cancelled project used to be the first row even next to a live issue.
  it("puts a cancelled project below a live issue instead of first", () => {
    const rows = buildSearchRows({
      query: "search",
      issues: [issue({ id: "i-live", title: "search live", status: "in_progress" })],
      projects: [project({ id: "p-dead", title: "search dead", status: "cancelled" })],
      recentIssues: [],
    });

    expect(shape(rows)).toEqual([
      "#search:groups.issues",
      "i-i-live",
      "#search:groups.cancelled",
      "p-p-dead",
    ]);
  });

  it("keeps live rows of both types above every cancelled row", () => {
    const rows = buildSearchRows({
      query: "search",
      issues: [
        issue({ id: "i-dead", number: 1, title: "search a", status: "cancelled" }),
        issue({ id: "i-live", number: 2, title: "search b", status: "todo" }),
        issue({ id: "i-done", number: 3, title: "search c", status: "done" }),
      ],
      projects: [
        project({ id: "p-dead", title: "search p1", status: "cancelled" }),
        project({ id: "p-live", title: "search p2", status: "planned" }),
      ],
      recentIssues: [],
    });

    expect(shape(rows)).toEqual([
      "#search:groups.projects",
      "p-p-live",
      "#search:groups.issues",
      // 'done' stays live — only cancelled work is demoted.
      "i-i-live",
      "i-i-done",
      "#search:groups.cancelled",
      "p-p-dead",
      "i-i-dead",
    ]);
  });

  it("keeps a cancelled direct hit at the top", () => {
    const rows = buildSearchRows({
      query: "MUL-7",
      issues: [issue({ id: "i-hit", number: 7, identifier: "MUL-7", status: "cancelled" })],
      projects: [],
      recentIssues: [],
    });

    expect(shape(rows)).toEqual(["#search:groups.issues", "i-i-hit"]);
  });

  it("omits the Cancelled section when nothing is demoted", () => {
    const rows = buildSearchRows({
      query: "search",
      issues: [issue({ id: "i1", status: "todo" })],
      projects: [project({ id: "p1", status: "completed" })],
      recentIssues: [],
    });
    expect(shape(rows)).toEqual(["#search:groups.projects", "p-p1", "#search:groups.issues", "i-i1"]);
  });
});
