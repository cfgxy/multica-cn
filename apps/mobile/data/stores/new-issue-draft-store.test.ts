// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * RUYI-79 last-assignee memory for the mobile new-issue form.
 *
 * Web parity (packages/core/issues/stores/draft-store.ts `lastAssignee*`):
 * the assignee submitted with a SUCCESSFUL create is remembered per
 * account(server) × workspace and used to prefill the next create form.
 * Unassigned is a remembered value too (Owner decision: align Web).
 * Selections that are never submitted must not touch the memory (web
 * clearDraft re-seeds from the memory, not from the discarded draft).
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
  const mod = await import("./new-issue-draft-store");
  // Store creation kicks off an async rehydrate; await it explicitly so
  // every test starts from the persisted backend state, not a race.
  await mod.useNewIssueLastAssigneeStore.persist.rehydrate();
  return mod;
}

beforeEach(() => {
  backend.reset();
});

describe("new-issue last-assignee memory (RUYI-79)", () => {
  it("remembers the submitted assignee and seeds it into a fresh draft", async () => {
    const mod = await freshStore();
    const value = { type: "agent" as const, id: "agent-1" };
    mod.setLastAssigneeFor("srv-1", "ws-a", value);

    expect(mod.getLastAssigneeFor("srv-1", "ws-a")).toEqual(value);

    const seed = await mod.seedDraftAssigneeFromMemory("srv-1", "ws-a");
    expect(seed).toBe(true);
    expect(mod.useNewIssueDraftStore.getState().assignee).toEqual(value);
  });

  it("remembers Unassigned as a value, not as no-history", async () => {
    const mod = await freshStore();
    mod.setLastAssigneeFor("srv-1", "ws-a", null);

    // null (remembered Unassigned) must be distinguishable from undefined
    // (no history) — both seed to an unassigned draft, but only null proves
    // a successful unassigned create was recorded.
    expect(mod.getLastAssigneeFor("srv-1", "ws-a")).toBeNull();

    const seed = await mod.seedDraftAssigneeFromMemory("srv-1", "ws-a");
    expect(seed).toBe(true);
    expect(mod.useNewIssueDraftStore.getState().assignee).toBeNull();
  });

  it("first use: no history — the draft stays at its unassigned default", async () => {
    const mod = await freshStore();
    expect(mod.getLastAssigneeFor("srv-1", "ws-a")).toBeUndefined();

    const seed = await mod.seedDraftAssigneeFromMemory("srv-1", "ws-a");
    expect(seed).toBe(false);
    expect(mod.useNewIssueDraftStore.getState().assignee).toBeNull();
  });

  it("workspaces are isolated: one slug's memory is invisible to another", async () => {
    const mod = await freshStore();
    const value = { type: "member" as const, id: "member-1" };
    mod.setLastAssigneeFor("srv-1", "ws-a", value);

    expect(mod.getLastAssigneeFor("srv-1", "ws-b")).toBeUndefined();
    const seed = await mod.seedDraftAssigneeFromMemory("srv-1", "ws-b");
    expect(seed).toBe(false);
    expect(mod.useNewIssueDraftStore.getState().assignee).toBeNull();
  });

  it("servers (accounts) are isolated; logout clears only the signed-out server", async () => {
    const mod = await freshStore();
    const onSrv1 = { type: "agent" as const, id: "agent-1" };
    const onSrv2 = { type: "squad" as const, id: "squad-2" };
    mod.setLastAssigneeFor("srv-1", "ws-a", onSrv1);
    mod.setLastAssigneeFor("srv-2", "ws-a", onSrv2);

    mod.clearServerMemory("srv-1");

    expect(mod.getLastAssigneeFor("srv-1", "ws-a")).toBeUndefined();
    expect(mod.getLastAssigneeFor("srv-2", "ws-a")).toEqual(onSrv2);
  });

  it("unsubmitted draft selections never enter memory", async () => {
    const mod = await freshStore();
    const draft = mod.useNewIssueDraftStore.getState();

    draft.setAssignee({ type: "member", id: "picked-but-not-submitted" });
    draft.reset();

    expect(mod.getLastAssigneeFor("srv-1", "ws-a")).toBeUndefined();
    expect(mod.useNewIssueDraftStore.getState().assignee).toBeNull();
  });

  it("seeding overwrites a leftover in-draft selection with the remembered one", async () => {
    const mod = await freshStore();
    const remembered = { type: "member" as const, id: "member-1" };
    mod.setLastAssigneeFor("srv-1", "ws-a", remembered);

    // Simulate a stale draft state (e.g. user picked someone, closed without
    // submitting; the next open must show the last SUBMITTED choice).
    mod.useNewIssueDraftStore
      .getState()
      .setAssignee({ type: "member", id: "stale-pick" });
    await mod.seedDraftAssigneeFromMemory("srv-1", "ws-a");

    expect(mod.useNewIssueDraftStore.getState().assignee).toEqual(remembered);
  });

  it("does not overwrite an assignee chosen while async memory hydration is pending", async () => {
    const mod = await freshStore();
    const remembered = { type: "agent" as const, id: "agent-1" };
    mod.setLastAssigneeFor("srv-1", "ws-a", remembered);
    const initialVersion = mod.useNewIssueDraftStore.getState().assigneeVersion;

    mod.useNewIssueDraftStore
      .getState()
      .setAssignee({ type: "member", id: "selected-after-open" });

    const seed = await mod.seedDraftAssigneeFromMemory(
      "srv-1",
      "ws-a",
      initialVersion,
    );

    expect(seed).toBe(false);
    expect(mod.useNewIssueDraftStore.getState().assignee).toEqual({
      type: "member",
      id: "selected-after-open",
    });
  });

  it("memory survives a cold start (re-import rehydrates from storage)", async () => {
    let mod = await freshStore();
    const value = { type: "agent" as const, id: "agent-1" };
    mod.setLastAssigneeFor("srv-1", "ws-a", value);
    // Let the persist middleware flush the write to the mocked backend.
    await mod.useNewIssueLastAssigneeStore.persist.rehydrate();

    mod = await freshStore();
    expect(mod.getLastAssigneeFor("srv-1", "ws-a")).toEqual(value);
  });
});
