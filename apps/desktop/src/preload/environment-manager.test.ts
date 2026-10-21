// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";

const ctx = vi.hoisted(() => ({
  expose: vi.fn(),
  invoke: vi.fn(),
  send: vi.fn(),
  sendSync: vi.fn(),
  on: vi.fn(),
  removeListener: vi.fn(),
}));

vi.mock("electron", () => ({
  contextBridge: { exposeInMainWorld: ctx.expose },
  ipcRenderer: {
    invoke: ctx.invoke,
    send: ctx.send,
    sendSync: ctx.sendSync,
    on: ctx.on,
    removeListener: ctx.removeListener,
  },
}));

vi.mock("@electron-toolkit/preload", () => ({ electronAPI: {} }));

describe("environment manager preload API", () => {
  beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    Object.defineProperty(process, "contextIsolated", { configurable: true, value: true });
  });

  it("exposes only the environment IPC methods and never exposes profile credentials", async () => {
    await import("./index");
    const daemonAPI = ctx.expose.mock.calls.find(([name]) => name === "daemonAPI")?.[1] as {
      environments: Record<string, (...args: unknown[]) => unknown>;
    };

    await daemonAPI.environments.previewArchive();
    await daemonAPI.environments.archive([{ environmentId: environmentID, revision }]);
    await daemonAPI.environments.restoreArchive(archiveID);

    expect(ctx.invoke).toHaveBeenNthCalledWith(1, "daemon:environment-archive-preview");
    expect(ctx.invoke).toHaveBeenNthCalledWith(2, "daemon:environment-archive", [{ environmentId: environmentID, revision }]);
    expect(ctx.invoke).toHaveBeenNthCalledWith(3, "daemon:environment-restore", archiveID);
    expect(Object.keys(daemonAPI.environments)).toEqual([
      "inventory", "previewCache", "cleanCache", "archives", "previewArchive", "archive", "restoreArchive",
    ]);
  });
});

const environmentID = "a".repeat(64);
const revision = "b".repeat(64);
const archiveID = "c".repeat(64);
