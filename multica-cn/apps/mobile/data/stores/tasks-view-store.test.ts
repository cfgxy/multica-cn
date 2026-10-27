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
 *   3. A workspace/connection switch lands on the target workspace's own
 *      memory: filters never ride across workspaces, and each workspace's
 *      last selection comes back on return (RUYI-531; supersedes item 12's
 *      wipe-on-switch).
 *   4. Filters (set + check ORDER) persist across a cold start, scoped to
 *      their owning workspace; the TAB comes back too (RUYI-531), the sort
 *      key stays session-scoped (RUYI-344 增量: the Owner's 记住筛选与顺序
 *      requirement).
 *   5. syncWorkspace(null) — the unresolved-workspace window on cold starts
 *      and server switches — must not wipe what hydration just restored.
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
    byWs: {},
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
    expect(s.byWs).toEqual({});
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

  // RUYI-531: a switch lands on the target workspace's own memory instead
  // of a wipe. Filters set in A must not appear in B (B has no memory →
  // clean default), and the round trip brings each side's own selection
  // back. The screen remounts with the new wsId already set, so the
  // transition is detected by the wsId stored in the state itself —
  // syncWorkspace is the screen-facing entry point.
  it("lands on each workspace's own filter memory across switches", () => {
    const { getState } = useTasksViewStore;
    // Workspace A: QA's repro shape — Blocked TAB + Medium priority chip.
    getState().syncWorkspace("ws-dev");
    getState().setTab("blocked");
    getState().togglePriorityFilter("medium");
    getState().toggleStatusFilter("done");
    expect(getState().wsId).toBe("ws-dev");

    // Switch to workspace B (never had filters): opens clean — filters must
    // NOT ride across. TAB survives (保 TAB), sort is untouched.
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

    // B sets its own choice; the round trip restores each side's own.
    getState().togglePriorityFilter("high");
    getState().syncWorkspace("ws-dev");
    s = getState();
    expect(s.wsId).toBe("ws-dev");
    expect(s.priorityFilters).toEqual(["medium"]);
    expect(s.statusFilters).toEqual(["done"]);
    expect(s.tab).toBe("blocked");

    getState().syncWorkspace("ws-backup");
    expect(getState().priorityFilters).toEqual(["high"]);
  });

  // RUYI-531 切换连接: servers live behind distinct workspace ids, so the
  // same per-workspace memory carries a connection round trip.
  it("keeps per-workspace memory across a connection (server) round trip", () => {
    const { getState } = useTasksViewStore;
    getState().syncWorkspace("srv-a-ws-1");
    getState().toggleStatusFilter("blocked");

    // Switch connection → server B's workspace opens clean.
    getState().syncWorkspace("srv-b-ws-9");
    expect(getState().statusFilters).toEqual([]);
    getState().togglePriorityFilter("urgent");

    // Back to server A: the selection set before the switch comes back.
    getState().syncWorkspace("srv-a-ws-1");
    expect(getState().statusFilters).toEqual(["blocked"]);
    expect(getState().priorityFilters).toEqual([]);

    getState().syncWorkspace("srv-b-ws-9");
    expect(getState().priorityFilters).toEqual(["urgent"]);
  });

  it("caps the per-workspace memory and evicts the oldest slot", () => {
    const { getState } = useTasksViewStore;
    getState().syncWorkspace("ws-0");
    getState().toggleStatusFilter("done");
    for (let i = 1; i <= 21; i += 1) {
      getState().syncWorkspace(`ws-${i}`);
      if (i === 19) getState().togglePriorityFilter("high");
    }
    // ws-0 (the oldest slot) gave way; a recent slot keeps its selection.
    getState().syncWorkspace("ws-0");
    expect(getState().statusFilters).toEqual([]);
    getState().syncWorkspace("ws-19");
    expect(getState().priorityFilters).toEqual(["high"]);
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

  it("restores the TAB across a cold start but keeps the sort session-scoped", async () => {
    vi.resetModules();
    const mod = await import("./tasks-view-store");
    const { getState } = mod.useTasksViewStore;
    getState().syncWorkspace("ws-dev");
    getState().setTab("blocked");
    getState().setSortBy("created_at");
    getState().toggleStatusFilter("done");

    const restarted = await coldStart();
    const s = restarted.useTasksViewStore.getState();
    // RUYI-531 验收 1: the segment TAB comes back; the sort key stays
    // session-scoped (RUYI-344), and the filter memory still works.
    expect(s.tab).toBe("blocked");
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

  it("keeps both workspaces' memory across a restart after a mid-session switch", async () => {
    const seeded = await seededStore();
    seeded.useTasksViewStore.getState().syncWorkspace("ws-other");
    expect(seeded.useTasksViewStore.getState().statusFilters).toEqual([]);

    const restarted = await coldStart();
    let s = restarted.useTasksViewStore.getState();
    // The restart lands in the last-active workspace with its own (empty)
    // view; ws-dev's selection survives in the per-workspace memory.
    expect(s.wsId).toBe("ws-other");
    expect(s.statusFilters).toEqual([]);

    // RUYI-531 切空间: switching back must not force a re-select — even
    // across the restart boundary.
    restarted.useTasksViewStore.getState().syncWorkspace("ws-dev");
    s = restarted.useTasksViewStore.getState();
    expect(s.statusFilters).toEqual(["blocked", "in_progress", "backlog"]);
  });

  // RUYI-531: on a cold start currentWorkspaceId resolves AFTER hydration
  // can land (the workspaces list is a network round trip) — the screen's
  // first syncWorkspace(null) used to wipe the just-restored memory.
  it("syncWorkspace(null) keeps the restored filters while the workspace is unresolved", async () => {
    await seededStore();
    const restarted = await coldStart();
    restarted.useTasksViewStore.getState().syncWorkspace(null);

    let s = restarted.useTasksViewStore.getState();
    expect(s.wsId).toBe("ws-dev");
    expect(s.statusFilters).toEqual(["blocked", "in_progress", "backlog"]);
    // …and the real id arriving later stays a same-workspace no-op.
    restarted.useTasksViewStore.getState().syncWorkspace("ws-dev");
    s = restarted.useTasksViewStore.getState();
    expect(s.statusFilters).toEqual(["blocked", "in_progress", "backlog"]);
  });

  it("migrates an RUYI-344-era blob without a per-workspace map", async () => {
    backend.map.set(
      "multica_mobile_tasks_view",
      JSON.stringify({
        state: {
          wsId: "ws-dev",
          statusFilters: ["done"],
          priorityFilters: [],
          mineRelations: { assigned: false, created: false, involved: false },
          assigneeRefs: [],
          includeNoAssignee: false,
          creatorRefs: [],
          agentRunning: false,
        },
        version: 0,
      }),
    );

    const restarted = await coldStart();
    const s = restarted.useTasksViewStore.getState();
    expect(s.wsId).toBe("ws-dev");
    expect(s.statusFilters).toEqual(["done"]);

    // The migrated memory round-trips a workspace switch.
    restarted.useTasksViewStore.getState().syncWorkspace("ws-x");
    restarted.useTasksViewStore.getState().syncWorkspace("ws-dev");
    expect(restarted.useTasksViewStore.getState().statusFilters).toEqual(["done"]);
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
