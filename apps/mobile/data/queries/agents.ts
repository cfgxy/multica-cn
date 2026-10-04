/**
 * Agent queries + cache-key factory (RUYI-346). The list previously lived at
 * the bare 2-segment key `["agents", wsId]`; agent management surfaces added
 * detail/webhook queries, so the keys moved to the three-segment shape that
 * matches web (`packages/core/inbox/queries.ts`) and the rest of mobile
 * (`issue-keys.ts`, `projects.ts`). TQ's prefix matching means
 * `invalidateQueries({ queryKey: agentKeys.all(wsId) })` clears the list AND
 * every detail/webhook cache under it — the WS layer only ever uses `.all`.
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const agentKeys = {
  all: (wsId: string | null) => ["agents", wsId] as const,
  list: (wsId: string | null) => [...agentKeys.all(wsId), "list"] as const,
  detail: (wsId: string | null, id: string) =>
    [...agentKeys.all(wsId), "detail", id] as const,
  // GET /api/agents/:id/webhooks — manager-gated payload behind one endpoint.
  webhooks: (wsId: string | null, id: string) =>
    [...agentKeys.all(wsId), "webhooks", id] as const,
};

export const agentListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: agentKeys.list(wsId),
    queryFn: ({ signal }) => api.listAgents({ signal }),
    enabled: !!wsId,
  });

// Agent detail — GET /api/agents/:id returns the same Agent shape as the
// list rows plus the full skill attachment list the settings screens edit.
// The empty-fallback sentinel (id: "") is the "not found / drift" marker the
// detail screen renders as its empty state.
export const agentDetailOptions = (wsId: string | null, agentId: string) =>
  queryOptions({
    queryKey: agentKeys.detail(wsId, agentId),
    queryFn: ({ signal }) => api.getAgent(agentId, { signal }),
    enabled: !!wsId && !!agentId,
  });

export const agentWebhooksOptions = (wsId: string | null, agentId: string) =>
  queryOptions({
    queryKey: agentKeys.webhooks(wsId, agentId),
    queryFn: ({ signal }) => api.listAgentWebhooks(agentId, { signal }),
    enabled: !!wsId && !!agentId,
  });
