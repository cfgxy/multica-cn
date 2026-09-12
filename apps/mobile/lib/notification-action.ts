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
  requestWorkspaceConfirmation: (
    action: WorkspaceConfirmation,
    onConfirm: () => Promise<void>,
  ) => void;
  requestServerConfirmation: (
    action: ServerConfirmation,
    onConfirm: () => Promise<void>,
  ) => void;
  showUnavailable: (action: UnavailableAction) => void;
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
      handlers.showUnavailable({ kind: "unavailable" });
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
    case "confirm-server":
      handlers.requestServerConfirmation(action, () =>
        openOnOtherServer(action, handlers),
      );
      return;
    case "unavailable":
      handlers.showUnavailable(action);
      return;
    case "drop":
      return;
  }
}
