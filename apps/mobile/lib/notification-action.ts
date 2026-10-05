import type { ServerSwitchOutcome } from "@/data/switch-server";
import type { NotificationAction } from "./notification-target";

export type WorkspaceActivationResult =
  | { kind: "ready" }
  | { kind: "not-member" }
  | { kind: "failed"; error: unknown };

type WorkspaceConfirmation = Extract<
  NotificationAction,
  { kind: "confirm-workspace" }
>;
type WorkspaceOpen = Extract<NotificationAction, { kind: "open" }>;
type WorkspaceAction = WorkspaceConfirmation | WorkspaceOpen;
type ServerConfirmation = Extract<
  NotificationAction,
  { kind: "confirm-server" }
>;
type UnavailableAction = Extract<
  NotificationAction,
  { kind: "unavailable" }
>;

export interface NotificationActionHandlers {
  navigate: (kind: "push" | "replace", route: string) => void;
  activateWorkspace: (
    workspaceSlug: string,
  ) => Promise<WorkspaceActivationResult>;
  switchServer: (serverId: string) => Promise<ServerSwitchOutcome>;
  /** Reachability probe for the notification's source server, resolved from
   *  the live server list. False when the entry is gone or the probe fails. */
  probeTargetServer: (serverId: string) => Promise<boolean>;
  requestWorkspaceConfirmation: (
    action: WorkspaceConfirmation,
    onConfirm: () => Promise<void>,
  ) => void;
  requestServerConfirmation: (
    action: ServerConfirmation,
    onConfirm: () => Promise<void>,
  ) => void;
  showUnavailable: (action: UnavailableAction) => void;
  /** Shown when the probe gate rejects the target before any switch: the
   *  user gets the real reason instead of a switch that rolls back. */
  showServerUnreachable: (
    action: ServerConfirmation,
    onRetry: () => Promise<void>,
  ) => void;
  showWorkspaceFailed: (error: unknown, onRetry: () => Promise<void>) => void;
  showServerFailed: (error: unknown, onRetry: () => Promise<void>) => void;
  onRetryAvailable: () => void;
}

async function openInWorkspace(
  action: WorkspaceAction,
  handlers: NotificationActionHandlers,
): Promise<void> {
  const activation = await handlers.activateWorkspace(action.workspaceSlug);
  if (activation.kind === "ready") {
    handlers.navigate("push", action.route);
    return;
  }
  if (activation.kind === "not-member") {
    handlers.navigate("replace", "/select-workspace");
    return;
  }
  handlers.showWorkspaceFailed(activation.error, () =>
    openInWorkspace(action, handlers),
  );
  handlers.onRetryAvailable();
}

async function activateServerWorkspace(
  action: ServerConfirmation,
  previousServerId: string,
  handlers: NotificationActionHandlers,
): Promise<void> {
  const activation = await handlers.activateWorkspace(action.workspaceSlug);
  if (activation.kind === "ready") {
    handlers.navigate("replace", action.route);
    return;
  }
  if (activation.kind === "not-member") {
    handlers.navigate("replace", "/select-workspace");
    return;
  }

  const rollback = await handlers.switchServer(previousServerId);
  const retry = () => openOnOtherServer(action, handlers);
  if (rollback.kind === "signed-in") {
    handlers.showWorkspaceFailed(activation.error, retry);
  } else if (
    rollback.kind === "signed-out" ||
    rollback.kind === "rollback-failed"
  ) {
    handlers.navigate("replace", "/login");
  } else if (rollback.kind === "failed") {
    handlers.showServerFailed(rollback.error, retry);
  } else {
    handlers.showServerFailed(
      new Error("Could not restore the previous server session."),
      retry,
    );
  }
  handlers.onRetryAvailable();
}

async function openOnOtherServer(
  action: ServerConfirmation,
  handlers: NotificationActionHandlers,
): Promise<void> {
  try {
    const outcome = await handlers.switchServer(action.serverId);
    const retry = () => openOnOtherServer(action, handlers);
    if (outcome.kind === "rollback-failed") {
      handlers.navigate("replace", "/login");
      return;
    }
    if (outcome.kind === "failed") {
      handlers.showServerFailed(outcome.error, retry);
      handlers.onRetryAvailable();
      return;
    }
    if (outcome.kind === "unavailable") {
      handlers.showUnavailable({
        kind: "unavailable",
        reason: "server-not-configured",
      });
      return;
    }
    if (outcome.kind === "signed-out") {
      handlers.navigate("replace", "/login");
      return;
    }
    await activateServerWorkspace(
      action,
      outcome.previousServerId,
      handlers,
    );
  } catch (error) {
    handlers.showServerFailed(error, () => openOnOtherServer(action, handlers));
    handlers.onRetryAvailable();
  }
}

export async function executeNotificationAction(
  action: NotificationAction,
  handlers: NotificationActionHandlers,
): Promise<void> {
  switch (action.kind) {
    case "open":
      await openInWorkspace(action, handlers);
      return;
    case "confirm-workspace":
      handlers.requestWorkspaceConfirmation(action, () =>
        openInWorkspace(action, handlers),
      );
      return;
    case "confirm-server": {
      // Probe the target BEFORE asking to switch: switchServer() rolls back
      // to the previous server when the target session can't be restored, so
      // an unreachable target turned every tap into "confirm a switch, watch
      // it bounce back, tap again" (RUYI-415). Same degrade semantics as the
      // cold-start picker (RUYI-404): an unreachable target never enters the
      // switch flow — it gets a retryable, named reason instead.
      const reachable = await handlers
        .probeTargetServer(action.serverId)
        .catch(() => false);
      if (!reachable) {
        const retry = () => executeNotificationAction(action, handlers);
        handlers.showServerUnreachable(action, retry);
        handlers.onRetryAvailable();
        return;
      }
      handlers.requestServerConfirmation(action, () =>
        openOnOtherServer(action, handlers),
      );
      return;
    }
    case "unavailable":
      handlers.showUnavailable(action);
      return;
    case "drop":
      return;
  }
}
