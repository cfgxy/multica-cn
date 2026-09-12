// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { QueryClient } from "@tanstack/react-query";

const state = vi.hoisted(() => ({
  activeServerId: "server-a",
  users: {} as Record<string, { id: string } | null>,
  workspaceSlugs: {} as Record<string, string | null>,
  tokens: {} as Record<string, string | null>,
  user: null as { id: string } | null,
  workspaceSlug: null as string | null,
  setActiveServer: vi.fn(),
  initialize: vi.fn(),
  setToken: vi.fn(),
  getToken: vi.fn(),
  setServerSwitching: vi.fn(),
  isServerSwitching: false,
}));

vi.mock("./api", () => ({ api: { setToken: state.setToken } }));
vi.mock("./auth-store", () => ({
  useAuthStore: {
    getState: () => ({
      user: state.user,
      initialize: state.initialize,
      setServerSwitching: state.setServerSwitching,
      isServerSwitching: state.isServerSwitching,
    }),
  },
}));
vi.mock("./server-store", () => ({
  useServerStore: {
    getState: () => ({
      activeServerId: state.activeServerId,
      setActiveServer: state.setActiveServer,
    }),
  },
}));
vi.mock("./secure-storage", () => ({ getToken: state.getToken }));
vi.mock("./workspace-store", () => ({
  useWorkspaceStore: {
    getState: () => ({ currentWorkspaceSlug: state.workspaceSlug }),
  },
}));

import { switchServer } from "./switch-server";

function queryClient() {
  return { clear: vi.fn() } as unknown as QueryClient;
}

beforeEach(() => {
  vi.clearAllMocks();
  state.activeServerId = "server-a";
  state.users = { "server-a": { id: "user-a" }, "server-b": null };
  state.workspaceSlugs = { "server-a": "acme", "server-b": null };
  state.tokens = { "server-a": "token-a", "server-b": null };
  state.user = { id: "user-a" };
  state.workspaceSlug = "acme";
  state.setActiveServer.mockImplementation(async (serverId: string) => {
    state.activeServerId = serverId;
  });
  state.initialize.mockImplementation(async () => {
    state.user = state.users[state.activeServerId] ?? null;
    state.workspaceSlug = state.workspaceSlugs[state.activeServerId] ?? null;
  });
  state.getToken.mockImplementation(async (serverId: string) => {
    return state.tokens[serverId] ?? null;
  });
  state.isServerSwitching = false;
  state.setServerSwitching.mockImplementation((isSwitching: boolean) => {
    state.isServerSwitching = isSwitching;
  });
});

describe("switchServer", () => {
  it("clears the old identity and restores the target server session", async () => {
    state.users["server-b"] = { id: "user-b" };
    state.workspaceSlugs["server-b"] = "other";
    const qc = queryClient();

    await expect(switchServer("server-b", qc)).resolves.toEqual({
      kind: "signed-in",
      slug: "other",
      previousServerId: "server-a",
    });

    expect(state.setToken).toHaveBeenCalledWith(null);
    expect(qc.clear).toHaveBeenCalledOnce();
    expect(state.activeServerId).toBe("server-b");
    expect(state.setServerSwitching).toHaveBeenNthCalledWith(1, true);
    expect(state.setServerSwitching).toHaveBeenLastCalledWith(false);
  });

  it("leaves the original identity untouched when persisting the selection fails", async () => {
    const error = new Error("storage unavailable");
    state.setActiveServer.mockRejectedValue(error);
    const qc = queryClient();

    await expect(switchServer("server-b", qc)).resolves.toEqual({
      kind: "failed",
      error,
    });

    expect(state.activeServerId).toBe("server-a");
    expect(state.setToken).not.toHaveBeenCalled();
    expect(qc.clear).not.toHaveBeenCalled();
    expect(state.setServerSwitching).toHaveBeenLastCalledWith(false);
  });

  it("reports a server removed after confirmation without clearing the session", async () => {
    state.setActiveServer.mockResolvedValue(undefined);
    const qc = queryClient();

    await expect(switchServer("server-b", qc)).resolves.toEqual({
      kind: "unavailable",
    });

    expect(state.activeServerId).toBe("server-a");
    expect(state.setToken).not.toHaveBeenCalled();
    expect(qc.clear).not.toHaveBeenCalled();
  });

  it("returns signed-out only when the target server has no restorable token", async () => {
    const qc = queryClient();

    await expect(switchServer("server-b", qc)).resolves.toEqual({
      kind: "signed-out",
    });

    expect(state.activeServerId).toBe("server-b");
  });

  it("restores the previous server when a retained target token cannot rebuild a session", async () => {
    state.tokens["server-b"] = "token-b";
    const qc = queryClient();

    const outcome = await switchServer("server-b", qc);

    expect(outcome.kind).toBe("failed");
    expect(state.activeServerId).toBe("server-a");
    expect(state.setActiveServer).toHaveBeenNthCalledWith(1, "server-b");
    expect(state.setActiveServer).toHaveBeenNthCalledWith(2, "server-a");
    expect(qc.clear).toHaveBeenCalledTimes(2);
  });

  it("treats a malformed successful target response with an empty user id as failed", async () => {
    state.users["server-b"] = { id: "" };
    state.tokens["server-b"] = "token-b";
    const qc = queryClient();

    await expect(switchServer("server-b", qc)).resolves.toMatchObject({
      kind: "failed",
    });

    expect(state.activeServerId).toBe("server-a");
  });

  it("reports rollback failure when the restored user has an empty id", async () => {
    state.tokens["server-b"] = "token-b";
    state.users["server-a"] = { id: "" };
    const qc = queryClient();

    await expect(switchServer("server-b", qc)).resolves.toMatchObject({
      kind: "rollback-failed",
    });
  });

  it("restores the previous server when initialization rejects unexpectedly", async () => {
    const error = new Error("unexpected initialization failure");
    state.initialize.mockImplementation(async () => {
      if (state.activeServerId === "server-b") throw error;
      state.user = state.users[state.activeServerId] ?? null;
      state.workspaceSlug = state.workspaceSlugs[state.activeServerId] ?? null;
    });
    const qc = queryClient();

    await expect(switchServer("server-b", qc)).resolves.toEqual({
      kind: "failed",
      error,
    });

    expect(state.activeServerId).toBe("server-a");
  });

  it("reports a rollback failure instead of claiming the prior session was restored", async () => {
    state.tokens["server-b"] = "token-b";
    state.setActiveServer.mockImplementation(async (serverId: string) => {
      if (serverId === "server-a") throw new Error("rollback storage unavailable");
      state.activeServerId = serverId;
    });
    const qc = queryClient();

    const outcome = await switchServer("server-b", qc);

    expect(outcome).toMatchObject({ kind: "rollback-failed" });
    expect(state.activeServerId).toBe("server-b");
  });

  it("rejects a concurrent switch without changing the active identity", async () => {
    state.users["server-b"] = { id: "user-b" };
    state.workspaceSlugs["server-b"] = "other";
    let beginInitialize!: () => void;
    let finishInitialize!: () => void;
    const initializeStarted = new Promise<void>((resolve) => {
      beginInitialize = resolve;
    });
    const initializeFinished = new Promise<void>((resolve) => {
      finishInitialize = resolve;
    });
    state.initialize.mockImplementation(async () => {
      beginInitialize();
      await initializeFinished;
      state.user = state.users[state.activeServerId] ?? null;
      state.workspaceSlug = state.workspaceSlugs[state.activeServerId] ?? null;
    });
    const qc = queryClient();

    const first = switchServer("server-b", qc);
    await initializeStarted;
    const second = await switchServer("server-c", qc);

    expect(second).toMatchObject({ kind: "failed" });
    expect(state.activeServerId).toBe("server-b");
    expect(state.isServerSwitching).toBe(true);
    expect(state.setActiveServer).toHaveBeenCalledTimes(1);
    expect(qc.clear).toHaveBeenCalledTimes(1);

    finishInitialize();
    await first;
    expect(state.isServerSwitching).toBe(false);
  });
});
