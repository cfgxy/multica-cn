import {
  parseArchiveResults,
  parseArchives,
  parseCachePreview,
  parseCacheResults,
  parseManagedEnvironments,
} from "../shared/environment-manager";

type IpcHandler = (_event: unknown, ...args: unknown[]) => unknown;

export interface EnvironmentIpcRegistrar {
  handle(channel: string, listener: IpcHandler): void;
}

export type EnvironmentRequest = (
  path: string,
  method?: string,
  body?: unknown,
) => Promise<unknown>;

function checkedSelections(value: unknown): Array<{ environment_id: string; revision: string }> {
  if (!Array.isArray(value) || value.length === 0 || value.length > 1000) {
    throw new Error("Select at least one environment");
  }
  return value.map((row) => {
    if (!row || typeof row !== "object") throw new Error("Invalid environment selection");
    const item = row as Record<string, unknown>;
    if (
      typeof item.environmentId !== "string" || !/^[0-9a-f]{64}$/.test(item.environmentId) ||
      typeof item.revision !== "string" || !/^[0-9a-f]{64}$/.test(item.revision)
    ) {
      throw new Error("Invalid environment selection");
    }
    return { environment_id: item.environmentId, revision: item.revision };
  });
}

export function registerEnvironmentManagerIpcHandlers(
  ipc: EnvironmentIpcRegistrar,
  request: EnvironmentRequest,
  createOperationID: () => string,
): void {
  ipc.handle("daemon:environment-inventory", async () =>
    parseManagedEnvironments(await request("/worktrees")),
  );
  ipc.handle("daemon:environment-cache-preview", async () =>
    parseCachePreview(await request("/worktrees", "POST", {
      action: "preview_cache",
      workspace_id: "",
      selections: [],
    })),
  );
  ipc.handle("daemon:environment-cache-clean", async (_event, selections: unknown) =>
    parseCacheResults(await request("/worktrees", "POST", {
      action: "clean_cache",
      workspace_id: "",
      selections: checkedSelections(selections),
    })),
  );
  ipc.handle("daemon:environment-archives", async () =>
    parseArchives(await request("/worktrees/archives")),
  );
  ipc.handle("daemon:environment-archive-preview", async () =>
    parseArchiveResults(await request("/worktrees/archives", "POST", {
      action: "preview",
      workspace_id: "",
      operation_id: "",
      archive_id: "",
      selections: [],
    })),
  );
  ipc.handle("daemon:environment-archive", async (_event, selections: unknown) =>
    parseArchiveResults(await request("/worktrees/archives", "POST", {
      action: "archive",
      workspace_id: "",
      operation_id: createOperationID(),
      archive_id: "",
      selections: checkedSelections(selections),
    })),
  );
  ipc.handle("daemon:environment-restore", async (_event, archiveID: unknown) => {
    if (typeof archiveID !== "string" || !/^[0-9a-f]{64}$/.test(archiveID)) {
      throw new Error("Invalid archive identity");
    }
    return parseArchiveResults(await request("/worktrees/archives", "POST", {
      action: "restore",
      workspace_id: "",
      operation_id: "",
      archive_id: archiveID,
      selections: [],
    }));
  });
}
