import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const workspaceListOptions = () =>
  queryOptions({
    queryKey: ["workspaces"] as const,
    queryFn: () => api.listWorkspaces(),
  });
