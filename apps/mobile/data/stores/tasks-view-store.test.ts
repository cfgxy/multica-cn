// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTasksViewStore } from "./tasks-view-store";

/**
 * View store behind the full-space Tasks tab (RUYI-344).
 *
 * Invariants guarded here:
 *   1. Switching quadrant TABs must NOT clear the status multi-select —
 *      the selection is owned by the "全部" tab and comes back when the
 *      user returns to it.
 *   2. clearFilters (sheet 重置) must NOT reset the current TAB or the
 *      sort choice — it clears filters only.
 *   3. A workspace switch must NOT carry filters across workspaces (item 12).
 *   4. Filters (set + check ORDER) persist across a cold start, scoped to
 *      their owning workspace; TAB and sort stay session-scoped (RUYI-344
 *      增量: the Owner's 记住筛选与顺序 requirement).
 */

const { backend } = vi.hoisted(() => {
  const map = new Map<string, string>();
  return {
    backend: {
      map,
      /** Delay reads so a test can observe hydration still in flight. */
      readDelayTicks: 0,
      reset() {
        map.clear();
        backend.readDelayTicks = 0;
      },
    },
  };
});

const tick = () => new Promise((r) => setTimeout(r, 0));

vi.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: async (k: string) => {
      const snapshot = backend.map.get(k) ?? null;
      for (let i = 0; i < backend.readDelayTicks; i += 1) await tick();
      return snapshot;
    },
    setItem: async (k: string, v: string) => {
      backend.map.set(k, v);
    },
    removeItem: async (k: string) => {
      backend.map.delete(k);
    },
  },
}));

function resetState() {
  useTasksViewStore.setState({
    tab: "all",
    wsId: null,
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

beforeEach(async () => {
  // Drain any in-flight hydration of a store imported by a previous test
  // before wiping the storage it may still write to.
  await tick();
  await tick();
  backend.reset();
  resetState();
});

describe("tasks view store", () => {
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

  // Item 12 (P1): filters set in workspace A must not appear in workspace
  // B. The screen remounts with the new wsId already set, so the transition
  // is detected by the wsId stored in the state itself — syncWorkspace is
  // the screen-facing entry point.
  it("clears filters on a workspace switch but keeps TAB and sort", () => {
    const { getState } = useTasksViewStore;
    // Workspace A: QA's repro shape — Blocked TAB + Medium priority chip.
    getState().syncWorkspace("ws-dev");
    getState().setTab("blocked");
    getState().togglePriorityFilter("medium");
    getState().toggleStatusFilter("done");
    expect(getState().wsId).toBe("ws-dev");

    // Switch to workspace B (never had filters): filters must clear,
    // TAB survives (保 TAB), sort is untouched.
    getState().syncWorkspace("ws-backup");
    let s = getState();
    expect(s.wsId).toBe("ws-backup");
    expect(s.priorityFilters).toEqual([]);
    expect(s.statusFilters).toEqual([]);
    expect(s.mineRelations).toEqual({
      assigned: false,
      created: false,
      involved: false,
    });
    expect(s.assigneeRefs).toEqual([]);
    expect(s.includeNoAssignee).toBe(false);
    expect(s.creatorRefs).toEqual([]);
    expect(s.agentRunning).toBe(false);
    expect(s.tab).toBe("blocked");
    expect(s.sortBy).toBe("updated_at");

    // Reverse trip: filters set in B must not ride back into A either.
    getState().togglePriorityFilter("high");
    getState().syncWorkspace("ws-dev");
    s = getState();
    expect(s.wsId).toBe("ws-dev");
    expect(s.priorityFilters).toEqual([]);
    expect(s.tab).toBe("blocked");
  });

  it("syncWorkspace is a no-op while the workspace is unchanged", () => {
    const { getState } = useTasksViewStore;
    getState().syncWorkspace("ws-dev");
    getState().togglePriorityFilter("high");
    getState().syncWorkspace("ws-dev");
    expect(getState().priorityFilters).toEqual(["high"]);
  });
});

/**
 * RUYI-344 增量: the cold-start memory. zustand persist over AsyncStorage;
 * the persisted set is owned by the wsId stored beside it, so a restart into
 * another workspace must NOT resurrect it — mid-session scoping (syncWorkspace)
 * and restart scoping (merge guard) have to agree.
 */
describe("tasks view persistence", () => {
  /** Fresh module import + completed hydration — what a restart loads. */
  async function coldStart() {
    vi.resetModules();
    const mod = await import("./tasks-view-store");
    await mod.useTasksViewStore.persist.rehydrate();
    await tick();
    return mod;
  }

  async function seededStore() {
    vi.resetModules();
    const mod = await import("./tasks-view-store");
    const { getState } = mod.useTasksViewStore;
    getState().syncWorkspace("ws-dev");
    // Owner's repro shape: 已阻塞 → 进行中 → 待规划, in that check order.
    getState().toggleStatusFilter("blocked");
    getState().toggleStatusFilter("in_progress");
    getState().toggleStatusFilter("backlog");
    return mod;
  }

  it("restores the filter set in check order after a cold start in the same workspace", async () => {
    const seeded = await seededStore();
    expect(seeded.useTasksViewStore.getState().statusFilters).toEqual([
      "blocked",
      "in_progress",
      "backlog",
    ]);

    const restarted = await coldStart();
    const s = restarted.useTasksViewStore.getState();
    expect(s.wsId).toBe("ws-dev");
    // Order is the feature: the array must come back exactly as checked.
    expect(s.statusFilters).toEqual(["blocked", "in_progress", "backlog"]);
  });

  it("restores the other filter dimensions beside the status order", async () => {
    vi.resetModules();
    const mod = await import("./tasks-view-store");
    const { getState } = mod.useTasksViewStore;
    getState().syncWorkspace("ws-dev");
    getState().toggleStatusFilter("done");
    getState().togglePriorityFilter("high");
    getState().toggleMineRelation("assigned");
    getState().setAssigneeRefs([{ type: "member", id: "u-1" }]);
    getState().setIncludeNoAssignee(true);
    getState().setCreatorRefs([{ type: "agent", id: "a-1" }]);
    getState().toggleAgentRunning();

    const restarted = await coldStart();
    const s = restarted.useTasksViewStore.getState();
    expect(s.priorityFilters).toEqual(["high"]);
    expect(s.mineRelations).toEqual({
      assigned: true,
      created: false,
      involved: false,
    });
    expect(s.assigneeRefs).toEqual([{ type: "member", id: "u-1" }]);
    expect(s.includeNoAssignee).toBe(true);
    expect(s.creatorRefs).toEqual([{ type: "agent", id: "a-1" }]);
    expect(s.agentRunning).toBe(true);
  });

  it("does not restore filters when the restart lands in another workspace (hydrate after mount)", async () => {
    const seeded = await seededStore();

    // A slow read keeps hydration in flight while the screen mounts into a
    // different workspace and runs syncWorkspace first — the real AsyncStorage
    // ordering on a cold start.
    backend.readDelayTicks = 3;
    vi.resetModules();
    const mod = await import("./tasks-view-store");
    mod.useTasksViewStore.getState().syncWorkspace("ws-other");
    for (let i = 0; i < 10; i += 1) await tick();

    const s = mod.useTasksViewStore.getState();
    expect(s.wsId).toBe("ws-other");
    expect(s.statusFilters).toEqual([]);
    expect(seeded.useTasksViewStore.getState().statusFilters).toEqual([
      "blocked",
      "in_progress",
      "backlog",
    ]);
  });

  it("does not restore filters when hydration lands before the screen mounts into another workspace", async () => {
    await seededStore();

    const restarted = await coldStart();
    // Hydration completed with ws-dev's filters live; the screen then mounts
    // into another workspace — the mid-session switch rule must clear them.
    restarted.useTasksViewStore.getState().syncWorkspace("ws-other");
    const s = restarted.useTasksViewStore.getState();
    expect(s.wsId).toBe("ws-other");
    expect(s.statusFilters).toEqual([]);
  });

  it("keeps TAB and sort session-scoped across a cold start", async () => {
    vi.resetModules();
    const mod = await import("./tasks-view-store");
    const { getState } = mod.useTasksViewStore;
    getState().syncWorkspace("ws-dev");
    getState().setTab("blocked");
    getState().setSortBy("created_at");
    getState().toggleStatusFilter("done");

    const restarted = await coldStart();
    const s = restarted.useTasksViewStore.getState();
    // TAB defaults back to 全部 and sort to updated_at (dispatch card: TAB
    // 本身不要求持久化，默认仍为全部), while the filter memory still works.
    expect(s.tab).toBe("all");
    expect(s.sortBy).toBe("updated_at");
    expect(s.statusFilters).toEqual(["done"]);
  });

  it("clearFilters empties the persisted set — no residue after a restart", async () => {
    const seeded = await seededStore();
    seeded.useTasksViewStore.getState().clearFilters();

    const restarted = await coldStart();
    const s = restarted.useTasksViewStore.getState();
    expect(s.wsId).toBe("ws-dev");
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

  it("persists a mid-session workspace switch cleared, not the old filters", async () => {
    const seeded = await seededStore();
    seeded.useTasksViewStore.getState().syncWorkspace("ws-other");
    expect(seeded.useTasksViewStore.getState().statusFilters).toEqual([]);

    const restarted = await coldStart();
    const s = restarted.useTasksViewStore.getState();
    // The last write wins: storage now belongs to ws-other with no filters,
    // so a restart must not resurrect ws-dev's selection.
    expect(s.wsId).toBe("ws-other");
    expect(s.statusFilters).toEqual([]);
  });

  it("ignores a persisted blob with no owning workspace", async () => {
    // A blob written before any workspace mounted carries wsId null — nothing
    // it holds may be restored, filters included.
    vi.resetModules();
    const mod = await import("./tasks-view-store");
    const { getState } = mod.useTasksViewStore;
    getState().toggleStatusFilter("done");
    // Force wsId back to null without going through syncWorkspace's clear —
    // the shape of a hand-edited / partially-written blob.
    mod.useTasksViewStore.setState({ wsId: null });
    await tick();

    const restarted = await coldStart();
    expect(restarted.useTasksViewStore.getState().statusFilters).toEqual([]);
  });
});
