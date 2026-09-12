// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RUYI-130 last-actor memory for the mobile smart-create (quick-create) flow.
 *
 * The reported defect: a create filed as the RUYI squad came back as its
 * leader agent (蔡小星) on the next open. Two independent causes, both
 * covered here:
 *   1. the memory only lived in module memory, so it never survived a cold
 *      start and the seed chain fell through to its "first visible agent"
 *      tail — which in that workspace IS the squad's leader;
 *   2. the memory was workspace-agnostic, so it could not be distinguished
 *      from another workspace's pick.
 * Seed-chain resolution itself is covered in lib/quick-create.test.ts — do
 * not re-run that matrix here.
 */

const { backend } = vi.hoisted(() => {
  const map = new Map<string, string>();
  return {
    backend: {
      map,
      /**
       * Writes land only after this many macrotask turns. A real
       * AsyncStorage write is a bridge round-trip, and the defect this file
       * guards is a caller that does not wait for it: with an instant mock
       * every unawaited write looks flushed, which is how the first cut of
       * these tests passed against a broken call chain (RUYI-130 rework).
       */
      writeDelayTicks: 0,
      /** Same, for reads — lets a test observe hydration still in flight. */
      readDelayTicks: 0,
      /** When set, getItem rejects — the "storage unreadable" branch. */
      failRead: false,
      reset() {
        map.clear();
        backend.writeDelayTicks = 0;
        backend.readDelayTicks = 0;
        backend.failRead = false;
      },
    },
  };
});

const tick = () => new Promise((r) => setTimeout(r, 0));

vi.mock("@react-native-async-storage/async-storage", () => ({
  __esModule: true,
  default: {
    getItem: async (k: string) => {
      if (backend.failRead) throw new Error("AsyncStorage read failed");
      // Snapshot first, THEN delay: a real bridge read captures the stored
      // value when the call crosses over, so a write that happens while the
      // read is in flight does not change what the read returns. Delaying
      // before the lookup would instead hand the reader the newer value and
      // hide the hydration-vs-clear race entirely.
      const snapshot = backend.map.get(k) ?? null;
      for (let i = 0; i < backend.readDelayTicks; i += 1) await tick();
      return snapshot;
    },
    setItem: async (k: string, v: string) => {
      for (let i = 0; i < backend.writeDelayTicks; i += 1) await tick();
      backend.map.set(k, v);
    },
    removeItem: async (k: string) => {
      for (let i = 0; i < backend.writeDelayTicks; i += 1) await tick();
      backend.map.delete(k);
    },
  },
}));

async function freshStore() {
  vi.resetModules();
  const mod = await import("./quick-create-prefs-store");
  await mod.ensureQuickCreateActorMemoryHydrated();
  return mod;
}

/** Re-import without rehydrating — what a cold start sees on disk. */
async function coldStart() {
  vi.resetModules();
  return import("./quick-create-prefs-store");
}

const SQUAD = { type: "squad" as const, id: "squad-ruyi" };
const AGENT = { type: "agent" as const, id: "agent-leader" };

beforeEach(() => {
  backend.reset();
});

describe("quick-create last-actor memory (RUYI-130)", () => {
  it("records the submitted actor and reads it back for the same server × workspace", async () => {
    const mod = await freshStore();
    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("keeps the squad type — never collapses it to an agent", async () => {
    const mod = await freshStore();
    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    const remembered = mod.getLastQuickCreateActor("srv-1", "ws-a");
    expect(remembered?.type).toBe("squad");
    expect(remembered).not.toEqual(AGENT);
  });

  it("survives a cold start — this is what the in-memory store lost", async () => {
    const first = await freshStore();
    await first.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);

    const restarted = await freshStore();
    expect(restarted.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("scopes by workspace: another workspace has no history", async () => {
    const mod = await freshStore();
    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-b")).toBeNull();
  });

  it("scopes by server: another account has no history", async () => {
    const mod = await freshStore();
    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    expect(mod.getLastQuickCreateActor("srv-2", "ws-a")).toBeNull();
  });

  it("overwrites the previous pick for the same server × workspace", async () => {
    const mod = await freshStore();
    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    await mod.setLastQuickCreateActor("srv-1", "ws-a", AGENT);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(AGENT);
  });

  it("clearQuickCreateActorMemory drops only the named server", async () => {
    const mod = await freshStore();
    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    await mod.setLastQuickCreateActor("srv-2", "ws-a", AGENT);

    await mod.clearQuickCreateActorMemory("srv-1");

    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toBeNull();
    expect(mod.getLastQuickCreateActor("srv-2", "ws-a")).toEqual(AGENT);
  });
});

/**
 * RUYI-130 rework. zustand's persist middleware returns the storage write's
 * promise from `set`, but `StoreApi.setState` is typed `void`, so the first
 * cut dropped it: the memory was only in RAM when the caller moved on.
 * These run against a storage whose writes take several turns, so an
 * unawaited chain is observably unflushed instead of accidentally fast.
 */
describe("persistence completion semantics", () => {
  it("awaiting the write is enough for the value to be on disk", async () => {
    backend.writeDelayTicks = 3;
    const mod = await freshStore();

    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);

    // Read the raw backend, not the live store: this is what a cold start
    // would find.
    expect(backend.map.size).toBe(1);
    const restarted = await coldStart();
    await restarted.ensureQuickCreateActorMemoryHydrated();
    expect(restarted.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("proves the delay is real: the value is NOT on disk before the write resolves", async () => {
    backend.writeDelayTicks = 3;
    const mod = await freshStore();

    const pending = mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    expect(backend.map.size).toBe(0);

    await pending;
    expect(backend.map.size).toBe(1);
  });

  it("rememberQuickCreateActorAfterSuccess resolves only after the write lands", async () => {
    backend.writeDelayTicks = 3;
    const mod = await freshStore();
    const ctx = {
      serverId: "srv-1",
      workspaceSlug: "ws-a",
      userId: "user-1",
      generation: 0,
    };

    await expect(
      mod.rememberQuickCreateActorAfterSuccess(ctx, ctx, SQUAD),
    ).resolves.toBe(true);

    // The submit handler awaits this before closing the screen, so by the
    // time the flow can be killed the pick is durable.
    const restarted = await coldStart();
    await restarted.ensureQuickCreateActorMemoryHydrated();
    expect(restarted.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("clearQuickCreateActorMemory resolves only after the removal lands", async () => {
    const mod = await freshStore();
    await mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    backend.writeDelayTicks = 3;

    await mod.clearQuickCreateActorMemory("srv-1");

    // logout/removeServer await this, so the signed-out account's pick is
    // gone from disk before the session teardown completes.
    const restarted = await coldStart();
    await restarted.ensureQuickCreateActorMemoryHydrated();
    expect(restarted.getLastQuickCreateActor("srv-1", "ws-a")).toBeNull();
  });

  it("a clear issued before hydration is not undone by the rehydrate that follows", async () => {
    // Seed disk, then come up fresh WITHOUT hydrating — the shape of a
    // logout that fires while the store is still reading. zustand's default
    // merge is persisted-state-wins, so a clear racing hydration would be
    // overwritten by the value it just removed.
    const seed = await freshStore();
    await seed.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);

    // A slow read keeps the store's own startup hydration genuinely in
    // flight while the clear is issued.
    backend.readDelayTicks = 3;
    const mod = await coldStart();
    expect(mod.useQuickCreateActorMemoryStore.persist.hasHydrated()).toBe(
      false,
    );

    await mod.clearQuickCreateActorMemory("srv-1");

    // Drain the read that was already in flight. Asserting before it lands
    // would pass either way — the restore is what that read DOES on arrival,
    // so the guard is only observable afterwards.
    for (let i = 0; i < 10; i += 1) await tick();

    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toBeNull();
    const restarted = await coldStart();
    await restarted.ensureQuickCreateActorMemoryHydrated();
    expect(restarted.getLastQuickCreateActor("srv-1", "ws-a")).toBeNull();
  });
});

describe("hydration status", () => {
  it("reports ready on a successful read", async () => {
    const mod = await coldStart();
    await expect(mod.ensureQuickCreateActorMemoryHydrated()).resolves.toBe(
      "ready",
    );
    expect(
      mod.useQuickCreateActorMemoryHydrationStore.getState().status,
    ).toBe("ready");
  });

  it("reports failed — not ready-and-empty — when the storage read throws", async () => {
    // zustand 5.0.12 catches the read error inside hydrate() and resolves
    // rehydrate() anyway, leaving hasHydrated() false. Awaiting the promise
    // alone therefore cannot tell a failure from an empty map, and treating
    // it as "no history" re-seeds the first visible agent (the reported bug).
    backend.failRead = true;
    const mod = await coldStart();

    await expect(mod.ensureQuickCreateActorMemoryHydrated()).resolves.toBe(
      "failed",
    );
    expect(
      mod.useQuickCreateActorMemoryHydrationStore.getState().status,
    ).toBe("failed");
  });

  it("a retry after a transient read failure reports ready", async () => {
    const seed = await freshStore();
    await seed.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);

    backend.failRead = true;
    const mod = await coldStart();
    expect(await mod.ensureQuickCreateActorMemoryHydrated()).toBe("failed");

    backend.failRead = false;
    expect(await mod.ensureQuickCreateActorMemoryHydrated()).toBe("ready");
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });
});

describe("rememberQuickCreateActorAfterSuccess", () => {
  const submitted = {
    serverId: "srv-1",
    workspaceSlug: "ws-a",
    userId: "user-1",
    generation: 0,
  };

  it("writes when the submitting context is still the active one", async () => {
    const mod = await freshStore();
    expect(
      await mod.rememberQuickCreateActorAfterSuccess(submitted, submitted, SQUAD),
    ).toBe(true);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("drops a late response that arrives after a workspace switch", async () => {
    const mod = await freshStore();
    expect(
      await mod.rememberQuickCreateActorAfterSuccess(
        submitted,
        { ...submitted, workspaceSlug: "ws-b" },
        SQUAD,
      ),
    ).toBe(false);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toBeNull();
  });

  it("drops a late response that arrives after an account or server change", async () => {
    const mod = await freshStore();
    expect(
      await mod.rememberQuickCreateActorAfterSuccess(
        submitted,
        { ...submitted, userId: "user-2" },
        SQUAD,
      ),
    ).toBe(false);
    expect(
      await mod.rememberQuickCreateActorAfterSuccess(
        submitted,
        { ...submitted, serverId: "srv-2" },
        SQUAD,
      ),
    ).toBe(false);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toBeNull();
  });

  it("drops a response whose context generation was invalidated", async () => {
    const mod = await freshStore();
    expect(
      await mod.rememberQuickCreateActorAfterSuccess(
        submitted,
        { ...submitted, generation: 1 },
        SQUAD,
      ),
    ).toBe(false);
  });

  it("drops the write entirely when there is no active context (signed out)", async () => {
    const mod = await freshStore();
    expect(
      await mod.rememberQuickCreateActorAfterSuccess(submitted, null, SQUAD),
    ).toBe(false);
  });
});
