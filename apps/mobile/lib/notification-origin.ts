import type { Workspace } from "@multica/core/types";
import type { ServerEntry } from "@/data/server-config";
import type { InboxNotificationOrigin } from "@/data/notifications/present-inbox-notification";
import { truncateLabel } from "./notification-target";

export function buildInboxNotificationOrigin({
  serverId,
  workspaceSlug,
  servers,
  workspaces,
}: {
  serverId: string;
  workspaceSlug: string | null;
  servers: readonly ServerEntry[];
  workspaces: readonly Workspace[] | undefined;
}): InboxNotificationOrigin {
  const activeServer = servers.find((server) => server.id === serverId);
  return {
    serverId,
    workspaceSlug: workspaceSlug ?? "",
    workspaceName: truncateLabel(
      workspaces?.find((workspace) => workspace.slug === workspaceSlug)?.name,
    ),
    serverName: truncateLabel(activeServer?.name),
  };
}
