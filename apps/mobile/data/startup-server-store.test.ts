// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  hydrate: vi.fn<() => Promise<string | null>>(),
  setActiveServer: vi.fn<() => Promise<void>>(),
}));

const servers = [
  { id: "default", name: "Built-in", apiUrl: "https://default.example.test", builtIn: true },
  { id: "srv_b", name: "Other", apiUrl: "https://other.example.test", builtIn: false },
];
const serverState = { servers, hydrate: mocks.hydrate, setActiveServer: mocks.setActiveServer };
vi.mock("./server-store", () => ({ useServerStore: { getState: () => serverState } }));

import { useStartupServerStore } from "./startup-server-store";

describe("mobile startup gate", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.setActiveServer.mockResolvedValue();
    serverState.servers = [...servers];
    useStartupServerStore.setState({ phase: "checking", previousId: null });
  });

  it("bypasses the gate with a single instance", async () => {
    serverState.servers = [servers[0]];
    mocks.hydrate.mockResolvedValue("default");
    await useStartupServerStore.getState().begin();
    expect(useStartupServerStore.getState().phase).toBe("ready");
  });

  it("uses a valid persisted target and waits for selection", async () => {
    mocks.hydrate.mockResolvedValue("srv_b");
    await useStartupServerStore.getState().begin();
    expect(useStartupServerStore.getState()).toMatchObject({ phase: "select", previousId: "srv_b" });
    expect(mocks.setActiveServer).not.toHaveBeenCalled();
    await useStartupServerStore.getState().connect("default");
    expect(mocks.setActiveServer).toHaveBeenCalledWith("default");
    expect(useStartupServerStore.getState().phase).toBe("ready");
  });

  it("keeps selection explicit for a removed previous target", async () => {
    mocks.hydrate.mockResolvedValue("srv_gone");
    await useStartupServerStore.getState().begin();
    expect(useStartupServerStore.getState()).toMatchObject({ phase: "select", previousId: null });
  });

  it("does not release the gate after a failed persistence write", async () => {
    mocks.hydrate.mockResolvedValue("default");
    mocks.setActiveServer.mockRejectedValue(new Error("storage failed"));
    await useStartupServerStore.getState().begin();
    await expect(useStartupServerStore.getState().connect("srv_b")).rejects.toThrow("storage failed");
    expect(useStartupServerStore.getState().phase).toBe("select");
  });
});
