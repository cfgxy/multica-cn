// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import {
  executeNotificationAction,
  type NotificationActionHandlers,
} from "./notification-action";

function createHarness() {
  let workspaceConfirmation: (() => Promise<void>) | undefined;
  let serverConfirmation: (() => Promise<void>) | undefined;
  let workspaceRetry: (() => Promise<void>) | undefined;
  let serverRetry: (() => Promise<void>) | undefined;

  const handlers: NotificationActionHandlers = {
    navigate: vi.fn(),
    activateWorkspace: vi.fn(async () => ({ kind: "ready" as const })),
    switchServer: vi.fn(async () => ({
      kind: "signed-in" as const,
      slug: "acme",
      previousServerId: "server-a",
    })),
    requestWorkspaceConfirmation: vi.fn((_action, onConfirm) => {
      workspaceConfirmation = onConfirm;
    }),
    requestServerConfirmation: vi.fn((_action, onConfirm) => {
      serverConfirmation = onConfirm;
    }),
    showUnavailable: vi.fn(),
    showWorkspaceFailed: vi.fn((_error, onRetry) => {
      workspaceRetry = onRetry;
    }),
    showServerFailed: vi.fn((_error, onRetry) => {
      serverRetry = onRetry;
    }),
    onRetryAvailable: vi.fn(),
  };

  return {
    handlers,
    workspaceConfirmation: () => workspaceConfirmation,
    serverConfirmation: () => serverConfirmation,
    workspaceRetry: () => workspaceRetry,
    serverRetry: () => serverRetry,
  };
}

describe("executeNotificationAction", () => {
  it("opens a same-workspace notification without confirmation", async () => {
    const { handlers } = createHarness();

    await executeNotificationAction(
      {
        kind: "open",
        route: "/(app)/acme/issue/issue-1",
        workspaceSlug: "acme",
      },
      handlers,
    );

    expect(handlers.activateWorkspace).toHaveBeenCalledWith("acme");
    expect(handlers.navigate).toHaveBeenCalledWith(
      "push",
      "/(app)/acme/issue/issue-1",
    );
  });

  it("routes a direct notification to the workspace selector after membership is revoked", async () => {
    const { handlers } = createHarness();
    vi.mocked(handlers.activateWorkspace).mockResolvedValue({
      kind: "not-member",
    });

    await executeNotificationAction(
      {
        kind: "open",
        route: "/(app)/acme/issue/issue-1",
        workspaceSlug: "acme",
      },
      handlers,
    );

    expect(handlers.navigate).toHaveBeenCalledWith(
      "replace",
      "/select-workspace",
    );
  });

  it("does not navigate when a workspace confirmation is cancelled", async () => {
    const harness = createHarness();

    await executeNotificationAction(
      {
        kind: "confirm-workspace",
        route: "/(app)/other/issue/issue-1",
        workspaceSlug: "other",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );

    expect(harness.handlers.requestWorkspaceConfirmation).toHaveBeenCalledOnce();
    expect(harness.handlers.navigate).not.toHaveBeenCalled();
    expect(harness.handlers.activateWorkspace).not.toHaveBeenCalled();
  });

  it("activates the confirmed workspace before pushing its issue route", async () => {
    const harness = createHarness();

    await executeNotificationAction(
      {
        kind: "confirm-workspace",
        route: "/(app)/other/issue/issue-1",
        workspaceSlug: "other",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.workspaceConfirmation()?.();

    expect(harness.handlers.activateWorkspace).toHaveBeenCalledWith("other");
    expect(harness.handlers.navigate).toHaveBeenCalledWith(
      "push",
      "/(app)/other/issue/issue-1",
    );
  });

  it("routes a confirmed non-member workspace to the selector", async () => {
    const harness = createHarness();
    vi.mocked(harness.handlers.activateWorkspace).mockResolvedValue({
      kind: "not-member",
    });

    await executeNotificationAction(
      {
        kind: "confirm-workspace",
        route: "/(app)/other/issue/issue-1",
        workspaceSlug: "other",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.workspaceConfirmation()?.();

    expect(harness.handlers.navigate).toHaveBeenCalledWith(
      "replace",
      "/select-workspace",
    );
  });

  it("routes a confirmed cross-server non-member workspace to the selector", async () => {
    const harness = createHarness();
    vi.mocked(harness.handlers.activateWorkspace).mockResolvedValue({
      kind: "not-member",
    });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.activateWorkspace).toHaveBeenCalledWith("other");
    expect(harness.handlers.navigate).toHaveBeenCalledWith(
      "replace",
      "/select-workspace",
    );
  });

  it("does not switch or navigate when a server confirmation is cancelled", async () => {
    const harness = createHarness();

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );

    expect(harness.handlers.requestServerConfirmation).toHaveBeenCalledOnce();
    expect(harness.handlers.switchServer).not.toHaveBeenCalled();
    expect(harness.handlers.navigate).not.toHaveBeenCalled();
  });

  it("switches a confirmed server before replacing the target issue route", async () => {
    const harness = createHarness();

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.switchServer).toHaveBeenCalledWith("server-b");
    expect(harness.handlers.activateWorkspace).toHaveBeenCalledWith("other");
    expect(harness.handlers.navigate).toHaveBeenCalledWith(
      "replace",
      "/other/issue/issue-1",
    );
  });

  it("keeps the user in place and enables retry after a server restore failure", async () => {
    const harness = createHarness();
    const error = new Error("offline");
    vi.mocked(harness.handlers.switchServer).mockResolvedValue({
      kind: "failed",
      error,
    });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.showServerFailed).toHaveBeenCalledWith(
      error,
      expect.any(Function),
    );
    expect(harness.handlers.onRetryAvailable).toHaveBeenCalledOnce();
    expect(harness.handlers.navigate).not.toHaveBeenCalled();
  });

  it("routes to login when the previous server session cannot be restored", async () => {
    const harness = createHarness();
    const error = new Error("rollback failed");
    vi.mocked(harness.handlers.switchServer).mockResolvedValue({
      kind: "rollback-failed",
      error,
    });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.navigate).toHaveBeenCalledWith("replace", "/login");
    expect(harness.handlers.showServerFailed).not.toHaveBeenCalled();
  });

  it("retries a failed server restore through the explicit retry action", async () => {
    const harness = createHarness();
    vi.mocked(harness.handlers.switchServer)
      .mockResolvedValueOnce({ kind: "failed", error: new Error("offline") })
      .mockResolvedValueOnce({
        kind: "signed-in",
        slug: "other",
        previousServerId: "server-a",
      });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();
    await harness.serverRetry()?.();

    expect(harness.handlers.switchServer).toHaveBeenCalledTimes(2);
    expect(harness.handlers.navigate).toHaveBeenCalledWith(
      "replace",
      "/other/issue/issue-1",
    );
  });

  it("restores the prior server before reporting a target workspace failure", async () => {
    const harness = createHarness();
    vi.mocked(harness.handlers.activateWorkspace).mockResolvedValueOnce({
      kind: "failed",
      error: new Error("workspace query failed"),
    });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.switchServer).toHaveBeenNthCalledWith(1, "server-b");
    expect(harness.handlers.switchServer).toHaveBeenNthCalledWith(2, "server-a");
    expect(harness.handlers.showWorkspaceFailed).toHaveBeenCalledWith(
      expect.any(Error),
      expect.any(Function),
    );
    expect(harness.handlers.navigate).not.toHaveBeenCalled();
  });

  it("routes to login when rollback fails after a target workspace lookup fails", async () => {
    const harness = createHarness();
    vi.mocked(harness.handlers.activateWorkspace).mockResolvedValue({
      kind: "failed",
      error: new Error("workspace query failed"),
    });
    vi.mocked(harness.handlers.switchServer)
      .mockResolvedValueOnce({
        kind: "signed-in",
        slug: "other",
        previousServerId: "server-a",
      })
      .mockResolvedValueOnce({
        kind: "rollback-failed",
        error: new Error("rollback failed"),
      });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.navigate).toHaveBeenCalledWith("replace", "/login");
    expect(harness.handlers.showWorkspaceFailed).not.toHaveBeenCalled();
    expect(harness.handlers.showServerFailed).not.toHaveBeenCalled();
  });

  it("routes a signed-out target server to login after confirmation", async () => {
    const harness = createHarness();
    vi.mocked(harness.handlers.switchServer).mockResolvedValue({
      kind: "signed-out",
    });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.navigate).toHaveBeenCalledWith("replace", "/login");
  });

  it("keeps the user in place when the confirmed source server is no longer configured", async () => {
    const harness = createHarness();
    vi.mocked(harness.handlers.switchServer).mockResolvedValue({
      kind: "unavailable",
    });

    await executeNotificationAction(
      {
        kind: "confirm-server",
        route: "/other/issue/issue-1",
        serverId: "server-b",
        workspaceSlug: "other",
        serverLabel: "Server B",
        workspaceLabel: "Other",
      },
      harness.handlers,
    );
    await harness.serverConfirmation()?.();

    expect(harness.handlers.showUnavailable).toHaveBeenCalledWith({
      kind: "unavailable",
      reason: "server-not-configured",
    });
    expect(harness.handlers.navigate).not.toHaveBeenCalled();
  });

  it("keeps unavailable notifications out of navigation", async () => {
    const harness = createHarness();

    await executeNotificationAction(
      { kind: "unavailable", reason: "missing-server-id" },
      harness.handlers,
    );

    expect(harness.handlers.showUnavailable).toHaveBeenCalledOnce();
    expect(harness.handlers.navigate).not.toHaveBeenCalled();
  });
});
