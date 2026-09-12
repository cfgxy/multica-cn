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
      reset() {
        map.clear();
      },
    },
  };
});

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

async function freshStore() {
  vi.resetModules();
  const mod = await import("./quick-create-prefs-store");
  await mod.useQuickCreateActorMemoryStore.persist.rehydrate();
  return mod;
}

const SQUAD = { type: "squad" as const, id: "squad-ruyi" };
const AGENT = { type: "agent" as const, id: "agent-leader" };

beforeEach(() => {
  backend.reset();
});

describe("quick-create last-actor memory (RUYI-130)", () => {
  it("records the submitted actor and reads it back for the same server × workspace", async () => {
    const mod = await freshStore();
    mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("keeps the squad type — never collapses it to an agent", async () => {
    const mod = await freshStore();
    mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    const remembered = mod.getLastQuickCreateActor("srv-1", "ws-a");
    expect(remembered?.type).toBe("squad");
    expect(remembered).not.toEqual(AGENT);
  });

  it("survives a cold start — this is what the in-memory store lost", async () => {
    const first = await freshStore();
    first.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    // Let the persist middleware flush before the process "restarts".
    await new Promise((r) => setTimeout(r, 0));

    const restarted = await freshStore();
    expect(restarted.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("scopes by workspace: another workspace has no history", async () => {
    const mod = await freshStore();
    mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-b")).toBeNull();
  });

  it("scopes by server: another account has no history", async () => {
    const mod = await freshStore();
    mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    expect(mod.getLastQuickCreateActor("srv-2", "ws-a")).toBeNull();
  });

  it("overwrites the previous pick for the same server × workspace", async () => {
    const mod = await freshStore();
    mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    mod.setLastQuickCreateActor("srv-1", "ws-a", AGENT);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(AGENT);
  });

  it("clearQuickCreateActorMemory drops only the named server", async () => {
    const mod = await freshStore();
    mod.setLastQuickCreateActor("srv-1", "ws-a", SQUAD);
    mod.setLastQuickCreateActor("srv-2", "ws-a", AGENT);

    mod.clearQuickCreateActorMemory("srv-1");

    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toBeNull();
    expect(mod.getLastQuickCreateActor("srv-2", "ws-a")).toEqual(AGENT);
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
      mod.rememberQuickCreateActorAfterSuccess(submitted, submitted, SQUAD),
    ).toBe(true);
    expect(mod.getLastQuickCreateActor("srv-1", "ws-a")).toEqual(SQUAD);
  });

  it("drops a late response that arrives after a workspace switch", async () => {
    const mod = await freshStore();
    expect(
      mod.rememberQuickCreateActorAfterSuccess(
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
      mod.rememberQuickCreateActorAfterSuccess(
        submitted,
        { ...submitted, userId: "user-2" },
        SQUAD,
      ),
    ).toBe(false);
    expect(
      mod.rememberQuickCreateActorAfterSuccess(
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
      mod.rememberQuickCreateActorAfterSuccess(
        submitted,
        { ...submitted, generation: 1 },
        SQUAD,
      ),
    ).toBe(false);
  });

  it("drops the write entirely when there is no active context (signed out)", async () => {
    const mod = await freshStore();
    expect(
      mod.rememberQuickCreateActorAfterSuccess(submitted, null, SQUAD),
    ).toBe(false);
  });
});
