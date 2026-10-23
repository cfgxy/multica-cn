// @vitest-environment node

import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDecisionsViewStore } from "./decisions-view-store";

/**
 * View store behind the Decisions tab (RUYI-530, refolded by RUYI-864) — the
 * screen kept only the decision list, so the store collapsed to one
 * dimension: the single-value status TAB (a server window selector, not a
 * client-side filter). The persisted 有推荐 / Agent 发起 sheet toggles and
 * their workspace-scoped AsyncStorage blob are gone with the filter sheet.
 *
 * Remaining conventions (Tasks-tab parity):
 *   1. The TAB is session-scoped — a cold start opens 全部.
 *   2. A workspace switch keeps the TAB (保 TAB, same convention as Tasks).
 *   3. Same-workspace sync is a no-op.
 */

function resetState() {
  useDecisionsViewStore.setState({
    tab: "all",
    wsId: null,
  });
}

beforeEach(() => {
  resetState();
});

describe("decisions view store (RUYI-864 single-window shape)", () => {
  it("defaults to the 全部 window", async () => {
    vi.resetModules();
    const { useDecisionsViewStore: fresh } = await import("./decisions-view-store");
    const s = fresh.getState();
    expect(s.tab).toBe("all");
    expect(s.wsId).toBeNull();
  });

  it("switches the window between all and the three card statuses", () => {
    const { getState } = useDecisionsViewStore;
    getState().setTab("open");
    expect(getState().tab).toBe("open");
    getState().setTab("cancelled");
    expect(getState().tab).toBe("cancelled");
    getState().setTab("all");
    expect(getState().tab).toBe("all");
  });

  it("keeps the TAB across a workspace switch (保 TAB, Tasks convention)", () => {
    const { getState } = useDecisionsViewStore;
    getState().syncWorkspace("ws-dev");
    getState().setTab("answered");
    expect(getState().wsId).toBe("ws-dev");

    getState().syncWorkspace("ws-backup");
    expect(getState().wsId).toBe("ws-backup");
    expect(getState().tab).toBe("answered");
  });

  it("same-workspace sync is a no-op", () => {
    const { getState } = useDecisionsViewStore;
    getState().syncWorkspace("ws-dev");
    getState().setTab("open");

    getState().syncWorkspace("ws-dev");
    expect(getState().tab).toBe("open");
    expect(getState().wsId).toBe("ws-dev");
  });
});
