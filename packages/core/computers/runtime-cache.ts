import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";

export function useRuntimeCache(userId: string) {
  const client = useQueryClient();
  const key = ["runtime-cache", userId];
  const cache = useQuery({
    queryKey: key,
    queryFn: () => api.getRuntimeCache(),
    enabled: !!userId,
    refetchInterval: 3000,
  });
  const refresh = () =>
    Promise.all([
      client.invalidateQueries({ queryKey: key }),
      client.invalidateQueries({ queryKey: ["computers", userId] }),
    ]);
  const check = useMutation({
    mutationFn: () => api.refreshRuntimeCache(),
    onSuccess: refresh,
  });
  const clear = useMutation({
    mutationFn: () => api.clearRuntimeCache(),
    onSuccess: refresh,
  });
  return { cache, check, clear };
}
