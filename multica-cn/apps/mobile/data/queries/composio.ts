/**
 * Composio connections query (RUYI-418 B3) — the viewer's own active
 * Composio connections, consumed by the owner-gated agent MCP-apps screen.
 * Mirrors packages/core/composio/queries.ts (same endpoint, same key shape);
 * the core version closes over the web cookie-auth client, which mobile
 * cannot reuse at runtime.
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const composioKeys = {
  all: ["composio"] as const,
  connections: () => [...composioKeys.all, "connections"] as const,
};

export const composioConnectionsOptions = () =>
  queryOptions({
    queryKey: composioKeys.connections(),
    queryFn: ({ signal }) => api.listComposioConnections({ signal }),
  });
