import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

/**
 * Per-agent full task list (active + terminal) — mobile mirror of web's
 * `agentTasksOptions` (packages/core/agents/queries.ts), same endpoint
 * GET /api/agents/{id}/tasks (server ListAgentTasks, private-agent access
 * gated). Powers the agent detail 活跃 tab's 运行历史 section (RUYI-538 ②);
 * the active half keeps reading the workspace snapshot so presence and the
 * per-agent list can't disagree about "is it working right now".
 *
 * WS task lifecycle events invalidate this family via use-presence-realtime
 * (web keeps the identical contract through its useRealtimeSync task-prefix
 * path).
 */
export const agentTasksKeys = {
  all: (wsId: string | null) => ["agent-tasks", wsId] as const,
  detail: (wsId: string | null, agentId: string) =>
    [...agentTasksKeys.all(wsId), agentId] as const,
};

export const agentTasksOptions = (wsId: string | null, agentId: string) =>
  queryOptions({
    queryKey: agentTasksKeys.detail(wsId, agentId),
    queryFn: ({ signal }) => api.listAgentTasks(agentId, { signal }),
    enabled: !!wsId && !!agentId,
  });
