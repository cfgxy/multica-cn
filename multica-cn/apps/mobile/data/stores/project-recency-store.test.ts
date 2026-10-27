// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RUYI-413 project-picker recency memory — device-local "which project did
 * I pick last, and when", keyed workspace × project, persisted in
 * AsyncStorage so the ordering survives a cold start.
 *
 * Locked here:
 *   - ordering contract of `sortProjectsByRecency`: selected projects by
 *     last-selection desc, never-selected after (alphabetical within each
 *     group), ties break alphabetically, other workspaces' timestamps
 *     don't leak in, null wsId degrades to plain alphabetical;
 *   - `recordProjectSelection` durability: it resolves only after the
 *     AsyncStorage write landed (the RUYI-130 lesson — an unawaited write
 *     is indistinguishable from a flushed one until the process dies);
 *   - no workspace id → no record;
 *   - persistence round-trip: a fresh module import rehydrates the map
 *     (the "杀进程重启 App 后顺序保留" acceptance clause).
 */

const { backend } = vi.hoisted(() => {
  const map = new Map<string, string>();
  return {
    backend: {
      map,
      /** Writes land only after this many macrotask turns (bridge round-trip). */
      writeDelayTicks: 0,
      reset() {
        map.clear();
        backend.writeDelayTicks = 0;
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
      for (let i = 0; i < backend.writeDelayTicks; i += 1) await tick();
      backend.map.set(k, v);
    },
    removeItem: async (k: string) => {
      backend.map.delete(k);
    },
  },
}));

type StoreModule = typeof import("./project-recency-store");

async function freshStore(): Promise<StoreModule> {
  vi.resetModules();
  const mod = await import("./project-recency-store");
  await mod.ensureProjectRecencyHydrated();
  return mod;
}

const WS = "ws-1";
const projects = [
  { id: "p-a", title: "Alpha" },
  { id: "p-b", title: "Bravo" },
  { id: "p-c", title: "Charlie" },
];

describe("sortProjectsByRecency", () => {
  it("puts selected projects first, most recent selection on top", async () => {
    const { sortProjectsByRecency } = await freshStore();
    const lastSelectedAt = {
      [`${WS}:p-a`]: "2026-10-04T09:00:00.000Z",
      [`${WS}:p-c`]: "2026-10-04T10:00:00.000Z",
    };
    expect(
      sortProjectsByRecency(projects, lastSelectedAt, WS).map((p) => p.id),
    ).toEqual(["p-c", "p-a", "p-b"]);
  });

  it("keeps never-selected projects alphabetical after the selected group", async () => {
    const { sortProjectsByRecency } = await freshStore();
    const lastSelectedAt = { [`${WS}:p-b`]: "2026-10-04T10:00:00.000Z" };
    expect(
      sortProjectsByRecency(projects, lastSelectedAt, WS).map((p) => p.id),
    ).toEqual(["p-b", "p-a", "p-c"]);
  });

  it("breaks equal timestamps alphabetically", async () => {
    const { sortProjectsByRecency } = await freshStore();
    const lastSelectedAt = {
      [`${WS}:p-c`]: "2026-10-04T10:00:00.000Z",
      [`${WS}:p-a`]: "2026-10-04T10:00:00.000Z",
    };
    expect(
      sortProjectsByRecency(projects, lastSelectedAt, WS).map((p) => p.id),
    ).toEqual(["p-a", "p-c", "p-b"]);
  });

  it("ignores timestamps recorded under another workspace", async () => {
    const { sortProjectsByRecency } = await freshStore();
    const lastSelectedAt = { ["ws-2:p-b"]: "2026-10-04T10:00:00.000Z" };
    expect(
      sortProjectsByRecency(projects, lastSelectedAt, WS).map((p) => p.id),
    ).toEqual(["p-a", "p-b", "p-c"]);
  });

  it("degrades to plain alphabetical when wsId is null", async () => {
    const { sortProjectsByRecency } = await freshStore();
    const lastSelectedAt = { [`${WS}:p-b`]: "2026-10-04T10:00:00.000Z" };
    expect(
      sortProjectsByRecency(projects, lastSelectedAt, null).map((p) => p.id),
    ).toEqual(["p-a", "p-b", "p-c"]);
  });

  it("does not mutate the input array", async () => {
    const { sortProjectsByRecency } = await freshStore();
    const input = [...projects];
    sortProjectsByRecency(
      input,
      { [`${WS}:p-c`]: "2026-10-04T10:00:00.000Z" },
      WS,
    );
    expect(input.map((p) => p.id)).toEqual(["p-a", "p-b", "p-c"]);
  });
});

describe("recordProjectSelection", () => {
  beforeEach(() => {
    backend.reset();
  });

  it("records the selection under workspace × project", async () => {
    const mod = await freshStore();
    await mod.recordProjectSelection(WS, "p-b");
    expect(mod.useProjectRecencyStore.getState().lastSelectedAt).toEqual({
      [`${WS}:p-b`]: expect.any(String),
    });
  });

  it("is a no-op without a workspace id", async () => {
    const mod = await freshStore();
    await mod.recordProjectSelection(null, "p-b");
    expect(mod.useProjectRecencyStore.getState().lastSelectedAt).toEqual({});
  });

  it("resolves only after the AsyncStorage write landed", async () => {
    const mod = await freshStore();
    backend.writeDelayTicks = 3;
    const pending = mod.recordProjectSelection(WS, "p-b");
    // The bridge write has not landed while the promise is in flight…
    expect([...backend.map.keys()]).toEqual([]);
    await pending;
    // …and promise resolution is exactly the write-landed signal.
    expect(backend.map.get("multica_mobile_project_recency")).toContain("p-b");
    expect(
      mod.useProjectRecencyStore.getState().lastSelectedAt[`${WS}:p-b`],
    ).toEqual(expect.any(String));
  });

  it("survives a fresh module import (cold start rehydration)", async () => {
    const mod = await freshStore();
    await mod.recordProjectSelection(WS, "p-b");
    const revived = await freshStore();
    const lastSelectedAt = revived.useProjectRecencyStore.getState()
      .lastSelectedAt;
    expect(lastSelectedAt).toHaveProperty(`${WS}:p-b`);
    expect(
      revived.sortProjectsByRecency(projects, lastSelectedAt, WS).map((p) => p.id),
    ).toEqual(["p-b", "p-a", "p-c"]);
  });
});
