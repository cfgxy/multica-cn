import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const vcsKeys = {
  all: (wsId: string) => ["vcs", wsId] as const,
  connections: (wsId: string) => [...vcsKeys.all(wsId), "connections"] as const,
  repositories: (wsId: string, connectionId: string) =>
    [...vcsKeys.all(wsId), "connections", connectionId, "repositories"] as const,
};

export const vcsConnectionsOptions = (wsId: string) =>
  queryOptions({
    queryKey: vcsKeys.connections(wsId),
    queryFn: () => api.listVCSConnections(wsId),
    enabled: !!wsId,
  });

export const gitLabRepositoriesOptions = (wsId: string, connectionId: string) =>
  infiniteQueryOptions({
    queryKey: vcsKeys.repositories(wsId, connectionId),
    queryFn: ({ pageParam }) => api.listGitLabRepositories(wsId, connectionId, pageParam),
    initialPageParam: 1,
    getNextPageParam: (lastPage) => lastPage.next_page ?? undefined,
    enabled: !!wsId && !!connectionId,
    retry: false,
  });
