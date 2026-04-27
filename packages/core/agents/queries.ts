import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const activeTasksKeys = {
  all: (wsId: string) => ["workspaces", wsId, "active-tasks"] as const,
  list: (wsId: string) => [...activeTasksKeys.all(wsId), "list"] as const,
};

// Workspace-scoped "live" tasks (active + recently failed) — the single
// shared source of truth that powers per-agent presence derivation across
// the app. By fetching once per workspace (rather than per-agent), all
// agent dots / hover cards / list rows derive presence from this cache
// with zero additional network traffic.
//
// The 30s staleTime is a safety net only; the primary freshness signal is
// WS task events, which invalidate this query immediately. Without WS,
// presence still updates within 30s on focus / mount.
export function activeTasksOptions(wsId: string) {
  return queryOptions({
    queryKey: activeTasksKeys.list(wsId),
    queryFn: () => api.getActiveTasksForWorkspace(),
    staleTime: 30 * 1000,
    gcTime: 5 * 60 * 1000,
    refetchOnWindowFocus: true,
  });
}
