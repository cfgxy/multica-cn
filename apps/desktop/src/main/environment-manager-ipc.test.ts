// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import { registerEnvironmentManagerIpcHandlers } from "./environment-manager-ipc";

type Handler = (_event: unknown, ...args: unknown[]) => unknown;

const handlers = new Map<string, Handler>();
const request = vi.fn();
const environmentID = "a".repeat(64);
const revision = "b".repeat(64);
const archiveID = "c".repeat(64);

function archiveResult() {
  return [{
    environment_id: environmentID,
    revision,
    archive_id: archiveID,
    reason: "",
    original_bytes: 2048,
    archive_bytes: 1024,
    reclaimed: true,
    restored: false,
  }];
}

async function invoke(channel: string, ...args: unknown[]) {
  const handler = handlers.get(channel);
  if (!handler) throw new Error(`Missing handler: ${channel}`);
  return handler({}, ...args);
}

describe("environment manager IPC", () => {
  beforeEach(() => {
    handlers.clear();
    request.mockReset();
    registerEnvironmentManagerIpcHandlers(
      { handle: (channel, handler) => handlers.set(channel, handler) },
      request,
      () => "d".repeat(64),
    );
  });

  it("uses the approved archive IPC handlers and preserves validated selections", async () => {
    request.mockResolvedValue(archiveResult());
    const selection = [{ environmentId: environmentID, revision }];

    await expect(invoke("daemon:environment-archive-preview")).resolves.toMatchObject([
      { environmentId: environmentID, revision },
    ]);
    await expect(invoke("daemon:environment-archive", selection)).resolves.toMatchObject([
      { archiveId: archiveID, reclaimed: true },
    ]);
    await expect(invoke("daemon:environment-restore", archiveID)).resolves.toMatchObject([
      { archiveId: archiveID },
    ]);

    expect(request).toHaveBeenNthCalledWith(1, "/worktrees/archives", "POST", {
      action: "preview", workspace_id: "", operation_id: "", archive_id: "", selections: [],
    });
    expect(request).toHaveBeenNthCalledWith(2, "/worktrees/archives", "POST", {
      action: "archive", workspace_id: "", operation_id: "d".repeat(64), archive_id: "",
      selections: [{ environment_id: environmentID, revision }],
    });
    expect(request).toHaveBeenNthCalledWith(3, "/worktrees/archives", "POST", {
      action: "restore", workspace_id: "", operation_id: "", archive_id: archiveID, selections: [],
    });
  });

  it("rejects an invalid archive ID before it reaches the daemon requester", async () => {
    await expect(invoke("daemon:environment-restore", "not-an-id")).rejects.toThrow("Invalid archive identity");
    expect(request).not.toHaveBeenCalled();
  });
});
