// @vitest-environment node

import { beforeEach, describe, expect, it } from "vitest";
import { useTasksViewStore } from "./tasks-view-store";

/**
 * View store behind the full-space Tasks tab (RUYI-344).
 *
 * Two invariants from the design spec are guarded here:
 *   1. Switching quadrant TABs must NOT clear the status multi-select —
 *      the selection is owned by the "全部" tab and comes back when the
 *      user returns to it.
 *   2. clearFilters (sheet 重置) must NOT reset the current TAB or the
 *      sort choice — it clears filters only.
 */

function resetState() {
  useTasksViewStore.setState({
    tab: "all",
    statusFilters: [],
    priorityFilters: [],
    mineRelations: { assigned: false, created: false, involved: false },
    assigneeRefs: [],
    includeNoAssignee: false,
    creatorRefs: [],
    agentRunning: false,
    sortBy: "updated_at",
  });
}

describe("tasks view store", () => {
  beforeEach(resetState);

  it("defaults to the 全部 tab with updated_at desc and no filters", async () => {
    const vitest = await import("vitest");
    const resetModules = vitest.vi.resetModules;
    resetModules();
    const { useTasksViewStore: fresh } = await import("./tasks-view-store");

    const s = fresh.getState();
    expect(s.tab).toBe("all");
    expect(s.sortBy).toBe("updated_at");
    expect(s.statusFilters).toEqual([]);
    expect(s.priorityFilters).toEqual([]);
    expect(s.mineRelations).toEqual({
      assigned: false,
      created: false,
      involved: false,
    });
    expect(s.assigneeRefs).toEqual([]);
    expect(s.includeNoAssignee).toBe(false);
    expect(s.creatorRefs).toEqual([]);
    expect(s.agentRunning).toBe(false);
  });

  it("keeps the status multi-select across TAB switches", () => {
    const { getState } = useTasksViewStore;
    getState().toggleStatusFilter("in_progress");

    getState().setTab("active");
    expect(getState().statusFilters).toEqual(["in_progress"]);

    getState().setTab("all");
    expect(getState().statusFilters).toEqual(["in_progress"]);
  });

  it("clearFilters clears filters but keeps the TAB and sort", () => {
    const { getState } = useTasksViewStore;
    getState().setTab("blocked");
    getState().setSortBy("created_at");
    getState().toggleStatusFilter("done");
    getState().togglePriorityFilter("high");
    getState().toggleMineRelation("assigned");
    getState().setAssigneeRefs([{ type: "member", id: "u-1" }]);
    getState().setIncludeNoAssignee(true);
    getState().setCreatorRefs([{ type: "agent", id: "a-1" }]);
    getState().toggleAgentRunning();

    getState().clearFilters();

    const s = getState();
    expect(s.tab).toBe("blocked");
    expect(s.sortBy).toBe("created_at");
    expect(s.statusFilters).toEqual([]);
    expect(s.priorityFilters).toEqual([]);
    expect(s.mineRelations).toEqual({
      assigned: false,
      created: false,
      involved: false,
    });
    expect(s.assigneeRefs).toEqual([]);
    expect(s.includeNoAssignee).toBe(false);
    expect(s.creatorRefs).toEqual([]);
    expect(s.agentRunning).toBe(false);
  });

  it("toggles a status key add/remove", () => {
    const { getState } = useTasksViewStore;
    getState().toggleStatusFilter("done");
    getState().toggleStatusFilter("blocked");
    expect(getState().statusFilters).toEqual(["done", "blocked"]);

    getState().toggleStatusFilter("done");
    expect(getState().statusFilters).toEqual(["blocked"]);
  });

  it("toggles a priority add/remove", () => {
    const { getState } = useTasksViewStore;
    getState().togglePriorityFilter("urgent");
    expect(getState().priorityFilters).toEqual(["urgent"]);

    getState().togglePriorityFilter("urgent");
    expect(getState().priorityFilters).toEqual([]);
  });

  it("toggles mine relations independently", () => {
    const { getState } = useTasksViewStore;
    getState().toggleMineRelation("created");
    expect(getState().mineRelations).toEqual({
      assigned: false,
      created: true,
      involved: false,
    });

    getState().toggleMineRelation("involved");
    getState().toggleMineRelation("created");
    expect(getState().mineRelations).toEqual({
      assigned: false,
      created: false,
      involved: true,
    });
  });

  it("replaces actor ref lists and flags wholesale", () => {
    const { getState } = useTasksViewStore;
    const refs = [
      { type: "member" as const, id: "u-1" },
      { type: "squad" as const, id: "s-1" },
    ];
    getState().setAssigneeRefs(refs);
    getState().setCreatorRefs([{ type: "agent" as const, id: "a-1" }]);
    getState().setIncludeNoAssignee(true);

    expect(getState().assigneeRefs).toEqual(refs);
    expect(getState().creatorRefs).toEqual([{ type: "agent", id: "a-1" }]);
    expect(getState().includeNoAssignee).toBe(true);

    getState().setAssigneeRefs([]);
    expect(getState().assigneeRefs).toEqual([]);
  });

  it("switches the sort key", () => {
    const { getState } = useTasksViewStore;
    getState().setSortBy("created_at");
    expect(getState().sortBy).toBe("created_at");

    getState().setSortBy("updated_at");
    expect(getState().sortBy).toBe("updated_at");
  });
});
