// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RUYI-78 per-issue read state (已读状态).
 *
 * Persisted locally via AsyncStorage so "this issue was opened once"
 * survives app restarts — the re-entry collapse policy in
 * timeline-list.tsx keys off exactly this store. In-memory session
 * stores (last-viewed / viewed-issues) deliberately lose cold-start
 * state; read state must not, or every cold start would silently
 * degrade the re-entry policy back to first-entry behavior.
 */

const { backend } = vi.hoisted(() => {
  const map = new Map<string, string>();
  return {
    backend: {
      map,
      calls: { setItem: 0 },
      reset() {
        map.clear();
        this.calls.setItem = 0;
      },
    },
  };
});

vi.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: async (k: string) => backend.map.get(k) ?? null,
    setItem: async (k: string, v: string) => {
      backend.calls.setItem += 1;
      backend.map.set(k, v);
    },
    removeItem: async (k: string) => {
      backend.map.delete(k);
    },
  },
}));

async function freshStore() {
  vi.resetModules();
  const mod = await import("./issue-read-state-store");
  const store = mod.useIssueReadStateStore;
  // Store creation kicks off an async rehydrate; await it explicitly so
  // every test starts from the persisted backend state, not a race.
  await store.persist.rehydrate();
  return mod;
}

beforeEach(() => {
  backend.reset();
});

describe("useIssueReadStateStore (RUYI-78 read state)", () => {
  it("an issue never opened is not read", async () => {
    const { useIssueReadStateStore: s } = await freshStore();
    expect(s.getState().isRead("issue-1")).toBe(false);
  });

  it("markRead makes the issue read and records the timestamp", async () => {
    const { useIssueReadStateStore: s } = await freshStore();
    s.getState().markRead("issue-1", "2026-09-05T10:00:00.000Z");
    expect(s.getState().isRead("issue-1")).toBe(true);
    expect(s.getState().readAt["issue-1"]).toBe("2026-09-05T10:00:00.000Z");
  });

  it("a re-mark overwrites with the newer timestamp", async () => {
    const { useIssueReadStateStore: s } = await freshStore();
    s.getState().markRead("issue-1", "2026-09-05T10:00:00.000Z");
    s.getState().markRead("issue-1", "2026-09-05T12:00:00.000Z");
    expect(s.getState().readAt["issue-1"]).toBe("2026-09-05T12:00:00.000Z");
  });

  it("read state is keyed per issue id", async () => {
    const { useIssueReadStateStore: s } = await freshStore();
    s.getState().markRead("issue-1", "2026-09-05T10:00:00.000Z");
    expect(s.getState().isRead("issue-2")).toBe(false);
  });

  it("persists across a cold start (re-import rehydrates from storage)", async () => {
    {
      const { useIssueReadStateStore: s } = await freshStore();
      s.getState().markRead("issue-1", "2026-09-05T10:00:00.000Z");
    }
    expect(backend.calls.setItem).toBeGreaterThan(0);
    const { useIssueReadStateStore: s } = await freshStore();
    expect(s.getState().isRead("issue-1")).toBe(true);
  });

  it("evicts the oldest entry beyond READ_STATE_MAX_ENTRIES", async () => {
    const { useIssueReadStateStore: s, READ_STATE_MAX_ENTRIES } =
      await freshStore();
    for (let i = 0; i < READ_STATE_MAX_ENTRIES; i++) {
      s
        .getState()
        .markRead(
          `issue-${i}`,
          new Date(Date.UTC(2026, 0, 1, 0, 0, 0, i)).toISOString(),
        );
    }
    s.getState().markRead("issue-new", "2026-12-31T00:00:00.000Z");
    const state = s.getState();
    expect(Object.keys(state.readAt).length).toBe(READ_STATE_MAX_ENTRIES);
    expect(state.isRead("issue-new")).toBe(true);
    // Oldest by timestamp is gone; a recent survivor remains.
    expect(state.isRead("issue-0")).toBe(false);
    expect(state.isRead(`issue-${READ_STATE_MAX_ENTRIES - 1}`)).toBe(true);
  });

  it("test reset clears state; the store keeps persisting afterwards", async () => {
    const { useIssueReadStateStore: s, resetIssueReadStateForTests } =
      await freshStore();
    s.getState().markRead("issue-1", "2026-09-05T10:00:00.000Z");
    resetIssueReadStateForTests();
    expect(s.getState().isRead("issue-1")).toBe(false);
    const writesBefore = backend.calls.setItem;
    s.getState().markRead("issue-2", "2026-09-05T11:00:00.000Z");
    expect(backend.calls.setItem).toBeGreaterThan(writesBefore);
  });
});
