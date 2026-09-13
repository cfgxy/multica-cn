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
}): InboxNotificationOrigin | null {
  const activeServer = servers.find((server) => server.id === serverId);
  const sourceWorkspace = workspaces?.find(
    (workspace) => workspace.id === workspaceId,
  );
  if (!sourceWorkspace) return null;

  return {
    serverId,
    workspaceSlug: sourceWorkspace.slug,
    workspaceName: truncateLabel(sourceWorkspace.name),
    serverName: truncateLabel(activeServer?.name),
  };
}
