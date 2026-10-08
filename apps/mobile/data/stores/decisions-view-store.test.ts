// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDecisionsViewStore } from "./decisions-view-store";

/**
 * View store behind the Decisions tab (RUYI-530) — the TAB/filter mechanics
 * mirror the Tasks tab invariants (tasks-view-store.test.ts):
 *   1. Switching status TABs must NOT clear the filters — they re-apply
 *      whatever TAB the user lands on (dispatch AC: 切换保留筛选状态).
 *   2. clearFilters (sheet 重置) must NOT reset the current TAB.
 *   3. A workspace switch must NOT carry filters across workspaces; TAB
 *      survives the switch (保 TAB, same convention as Tasks).
 *   4. Filters persist across a cold start scoped to their owning
 *      workspace; the TAB stays session-scoped.
 */

const { backend } = vi.hoisted(() => {
  const map = new Map<string, string>();
  return {
    backend: {
      map,
      reset() {
        map.clear();
      },
    },
  };
});

const tick = () => new Promise((r) => setTimeout(r, 0));

vi.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: async (k: string) => backend.map.get(k) ?? null,
    setItem: async (k: string, v: string) => {
      backend.map.set(k, v);
    },
    removeItem: async (k: string) => {
      backend.map.delete(k);
    },
  },
}));

function resetState() {
  useDecisionsViewStore.setState({
    tab: "all",
    wsId: null,
    recommendedOnly: false,
    agentCreatedOnly: false,
  });
}

beforeEach(async () => {
  await tick();
  await tick();
  backend.reset();
  resetState();
});

describe("decisions view store", () => {
  it("defaults to the 全部 tab with no filters", async () => {
    const vitest = await import("vitest");
    vitest.vi.resetModules();
    const { useDecisionsViewStore: fresh } = await import("./decisions-view-store");

    const s = fresh.getState();
    expect(s.tab).toBe("all");
    expect(s.recommendedOnly).toBe(false);
    expect(s.agentCreatedOnly).toBe(false);
  });

  it("keeps filters across TAB switches (dispatch AC: 切换保留筛选状态)", () => {
    const { getState } = useDecisionsViewStore;
    getState().toggleRecommendedOnly();
    getState().toggleAgentCreatedOnly();

    getState().setTab("answered");
    expect(getState().recommendedOnly).toBe(true);
    expect(getState().agentCreatedOnly).toBe(true);

    getState().setTab("all");
    expect(getState().recommendedOnly).toBe(true);
    expect(getState().agentCreatedOnly).toBe(true);
  });

  it("toggles both filters independently add/remove", () => {
    const { getState } = useDecisionsViewStore;
    getState().toggleRecommendedOnly();
    expect(getState().recommendedOnly).toBe(true);
    expect(getState().agentCreatedOnly).toBe(false);

    getState().toggleAgentCreatedOnly();
    expect(getState().recommendedOnly).toBe(true);
    expect(getState().agentCreatedOnly).toBe(true);

    getState().toggleRecommendedOnly();
    expect(getState().recommendedOnly).toBe(false);
    expect(getState().agentCreatedOnly).toBe(true);
  });

  it("clearFilters clears filters but keeps the TAB", () => {
    const { getState } = useDecisionsViewStore;
    getState().setTab("cancelled");
    getState().toggleRecommendedOnly();
    getState().toggleAgentCreatedOnly();

    getState().clearFilters();

    const s = getState();
    expect(s.tab).toBe("cancelled");
    expect(s.recommendedOnly).toBe(false);
    expect(s.agentCreatedOnly).toBe(false);
  });

  it("clears filters on a workspace switch but keeps the TAB", () => {
    const { getState } = useDecisionsViewStore;
    getState().syncWorkspace("ws-dev");
    getState().setTab("open");
    getState().toggleRecommendedOnly();
    expect(getState().wsId).toBe("ws-dev");

    getState().syncWorkspace("ws-backup");
    const s = getState();
    expect(s.wsId).toBe("ws-backup");
    expect(s.recommendedOnly).toBe(false);
    expect(s.agentCreatedOnly).toBe(false);
    expect(s.tab).toBe("open");
  });

  it("same-workspace sync is a no-op", () => {
    const { getState } = useDecisionsViewStore;
    getState().syncWorkspace("ws-dev");
    getState().setTab("open");
    getState().toggleRecommendedOnly();

    getState().syncWorkspace("ws-dev");
    expect(getState().tab).toBe("open");
    expect(getState().recommendedOnly).toBe(true);
  });

  // Persisted filters restore only under their owning workspace; the TAB
  // never rides in the blob (session-scoped, same as Tasks).
  it("restores persisted filters for the same workspace on a cold start", async () => {
    const vitest = await import("vitest");
    const { getState } = useDecisionsViewStore;
    getState().syncWorkspace("ws-dev");
    getState().toggleRecommendedOnly();
    getState().setTab("answered");
    await tick(); // let persist write

    vitest.vi.resetModules();
    const { useDecisionsViewStore: fresh } = await import("./decisions-view-store");
    fresh.getState().syncWorkspace("ws-dev");
    await tick();
    await tick();

    const s = fresh.getState();
    expect(s.recommendedOnly).toBe(true);
    expect(s.agentCreatedOnly).toBe(false);
    // TAB stays session-scoped — the fresh store opens on 全部.
    expect(s.tab).toBe("all");
  });

  it("rejects a blob written for another workspace when already mounted elsewhere", async () => {
    const vitest = await import("vitest");
    const { getState } = useDecisionsViewStore;
    getState().syncWorkspace("ws-dev");
    getState().toggleAgentCreatedOnly();
    await tick();

    const blob = backend.map.get("multica_mobile_decisions_view")!;
    expect(typeof blob).toBe("string");

    vitest.vi.resetModules();
    const { useDecisionsViewStore: fresh } = await import("./decisions-view-store");
    // Screen mounts into a different workspace BEFORE hydration lands.
    fresh.getState().syncWorkspace("ws-other");
    // Replay the old blob through AsyncStorage.
    backend.map.set("multica_mobile_decisions_view", blob);
    await tick();
    await tick();

    expect(fresh.getState().agentCreatedOnly).toBe(false);
  });
});
