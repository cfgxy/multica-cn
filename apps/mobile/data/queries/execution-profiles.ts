import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const executionProfileKeys = {
  all: (wsId: string | null) => ["execution-profiles", wsId] as const,
  list: (wsId: string | null) => [...executionProfileKeys.all(wsId), "list"] as const,
  detail: (wsId: string | null, profileId: string) =>
    [...executionProfileKeys.all(wsId), "detail", profileId] as const,
};

export const executionProfileListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: executionProfileKeys.list(wsId),
    queryFn: ({ signal }) => api.listExecutionProfiles(wsId as string, { signal }),
    enabled: !!wsId,
  });

export const executionProfileDetailOptions = (
  wsId: string | null,
  profileId: string,
) =>
  queryOptions({
    queryKey: executionProfileKeys.detail(wsId, profileId),
    queryFn: ({ signal }) =>
      api.getExecutionProfile(wsId as string, profileId, { signal }),
    enabled: !!wsId && !!profileId,
  });
