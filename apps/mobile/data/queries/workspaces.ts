import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const workspaceListOptions = () =>
  queryOptions({
    queryKey: ["workspaces"] as const,
    queryFn: ({ signal }) => api.listWorkspaces({ signal }),
  });

/** Use after an explicit navigation confirmation to re-check live membership. */
export const freshWorkspaceListOptions = () => ({
  ...workspaceListOptions(),
  staleTime: 0,
});
