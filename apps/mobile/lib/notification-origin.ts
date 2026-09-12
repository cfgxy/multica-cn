import type { Workspace } from "@multica/core/types";
import type { ServerEntry } from "@/data/server-config";
import type { InboxNotificationOrigin } from "@/data/notifications/present-inbox-notification";
import { truncateLabel } from "./notification-target";

export function buildInboxNotificationOrigin({
  serverId,
  workspaceId,
  servers,
  workspaces,
}: {
  serverId: string;
  workspaceId: string | null | undefined;
  servers: readonly ServerEntry[];
  workspaces: readonly Workspace[] | undefined;
}): InboxNotificationOrigin {
  const activeServer = servers.find((server) => server.id === serverId);
  const sourceWorkspace = workspaces?.find(
    (workspace) => workspace.id === workspaceId,
  );
  return {
    serverId,
    // An unknown source workspace must not borrow the active slug. A linkless
    // notification is recoverable; a mismatched issue and workspace can 404.
    workspaceSlug: sourceWorkspace?.slug ?? "",
    workspaceName: truncateLabel(sourceWorkspace?.name),
    serverName: truncateLabel(activeServer?.name),
  };
}
