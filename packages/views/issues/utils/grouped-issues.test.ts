import { describe, it, expect } from "vitest";
import type { Issue, IssueAssigneeGroup } from "@multica/core/types";
import { applyWorkingFilterToGroups } from "./grouped-issues";

function makeIssue(id: string): Issue {
  return {
    id,
    workspace_id: "ws-1",
    number: Number(id) || 0,
    identifier: `MUL-${id}`,
    title: id,
    description: null,
    status: "todo",
    priority: "medium",
    assignee_type: null,
    assignee_id: null,
    creator_type: "member",
    creator_id: "u-1",
    parent_issue_id: null,
    project_id: null,
    position: 0,
    start_date: null,
    due_date: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function makeGroup(id: string, issues: Issue[], total = issues.length): IssueAssigneeGroup {
  return {
    id,
    assignee_type: "member",
    assignee_id: id,
    issues,
    total,
  };
}

describe("applyWorkingFilterToGroups", () => {
  it("returns the same reference when workingOnly is off", () => {
    const groups = [makeGroup("g1", [makeIssue("1"), makeIssue("2")])];
    const result = applyWorkingFilterToGroups(groups, false, new Set(["1"]));
    expect(result).toBe(groups);
  });

  it("returns undefined passthrough when groups is undefined", () => {
    expect(applyWorkingFilterToGroups(undefined, true, new Set(["1"]))).toBeUndefined();
  });

  it("collapses every group to empty when workingIssueIds is undefined", () => {
    // While the snapshot query is still loading we should not pretend any
    // issue is working — otherwise stale cards would leak through the filter.
    const groups = [makeGroup("g1", [makeIssue("1"), makeIssue("2")], 5)];
    const result = applyWorkingFilterToGroups(groups, true, undefined);
    expect(result).toEqual([{ ...groups[0]!, issues: [], total: 0 }]);
  });

  it("filters issues and rewrites total to the filtered count", () => {
    const groups = [
      makeGroup("g1", [makeIssue("1"), makeIssue("2"), makeIssue("3")], 10),
    ];
    const result = applyWorkingFilterToGroups(
      groups,
      true,
      new Set(["1", "3"]),
    );
    expect(result).toBeDefined();
    expect(result![0]!.issues.map((i) => i.id)).toEqual(["1", "3"]);
    // total must follow the visible count so the column header doesn't keep
    // showing the unfiltered cache total when the user toggles Working.
    expect(result![0]!.total).toBe(2);
  });

  it("keeps a group with zero matches instead of dropping it", () => {
    // Removing the column on every toggle would shift the board layout.
    const groups = [
      makeGroup("g1", [makeIssue("1"), makeIssue("2")], 7),
      makeGroup("g2", [makeIssue("3")], 1),
    ];
    const result = applyWorkingFilterToGroups(groups, true, new Set(["3"]));
    expect(result).toBeDefined();
    expect(result![0]!.id).toBe("g1");
    expect(result![0]!.issues).toEqual([]);
    expect(result![0]!.total).toBe(0);
    expect(result![1]!.issues.map((i) => i.id)).toEqual(["3"]);
    expect(result![1]!.total).toBe(1);
  });
});
