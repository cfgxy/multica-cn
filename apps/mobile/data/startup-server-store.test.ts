// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  hydrate: vi.fn<() => Promise<string | null>>(),
  setActiveServer: vi.fn<() => Promise<void>>(),
  probeServer: vi.fn<(apiUrl: string, signal: AbortSignal) => Promise<boolean>>(),
}));

const servers = [
  { id: "default", name: "Built-in", apiUrl: "https://default.example.test", builtIn: true },
  { id: "srv_b", name: "Other", apiUrl: "https://other.example.test", builtIn: false },
];
const serverState = { servers, hydrate: mocks.hydrate, setActiveServer: mocks.setActiveServer };
vi.mock("./server-store", () => ({ useServerStore: { getState: () => serverState } }));
vi.mock("./probe-server", () => ({ probeServer: mocks.probeServer }));

import { useStartupServerStore } from "./startup-server-store";

describe("mobile startup gate", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.setActiveServer.mockResolvedValue();
    mocks.probeServer.mockResolvedValue(true);
    serverState.servers = [...servers];
    useStartupServerStore.setState({ phase: "checking", previousId: null });
  });

  it("bypasses the gate with a single instance", async () => {
    serverState.servers = [servers[0]];
    mocks.hydrate.mockResolvedValue("default");
    await useStartupServerStore.getState().begin();
    expect(useStartupServerStore.getState().phase).toBe("ready");
  });

  it("auto-connects a valid persisted target without parking at the picker", async () => {
    // RUYI-346 round 3: the picker mounts only through navigation, and any
    // navigation before the gate settles discards a cold-start deep link.
    // A resolvable startup target must converge by itself — the gate holds
    // at "checking" (no navigation) until connect completes.
    mocks.hydrate.mockResolvedValue("srv_b");
    await useStartupServerStore.getState().begin();
    expect(mocks.setActiveServer).toHaveBeenCalledWith("srv_b");
    expect(useStartupServerStore.getState()).toMatchObject({ phase: "ready", previousId: null });
  });

  it("degrades to explicit selection when the persisted target is unreachable", async () => {
    // RUYI-404: connect() only persists the choice locally and always
    // resolves, so an unreachable target never rejects on its own. The gate
    // must observe reachability itself (probe, bounded by
    // STARTUP_PROBE_TIMEOUT_MS) and converge to the picker within finite
    // time instead of releasing startup into a silent hang. Persistence
    // resolves normally and no network request runs in this test — the mock
    // probe result is the only reachability signal.
    mocks.hydrate.mockResolvedValue("srv_b");
    mocks.probeServer.mockResolvedValue(false);
    await useStartupServerStore.getState().begin();
    expect(mocks.probeServer).toHaveBeenCalledWith(
      "https://other.example.test",
      expect.anything(),
    );
    expect(mocks.setActiveServer).not.toHaveBeenCalled();
    expect(useStartupServerStore.getState()).toMatchObject({
      phase: "select",
      previousId: "srv_b",
    });
  });

  it("falls back to explicit selection when the startup auto-connect fails", async () => {
    mocks.hydrate.mockResolvedValue("srv_b");
    mocks.setActiveServer.mockRejectedValue(new Error("unreachable"));
    await useStartupServerStore.getState().begin();
    expect(mocks.setActiveServer).toHaveBeenCalledWith("srv_b");
    expect(useStartupServerStore.getState()).toMatchObject({ phase: "select", previousId: "srv_b" });
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
