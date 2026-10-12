import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ComputerLifecycleInput } from "./schema";

export function useLinuxUserDetail(
  userId: string,
  bindingId: string,
  admin = false,
) {
  const client = useQueryClient();
  const key = [admin ? "computer-admin" : "computers", userId];
  const operations = useQuery({
    queryKey: [...key, "operations", bindingId],
    queryFn: () => api.listComputerOperations(bindingId, admin),
    enabled: !!userId && !!bindingId,
    refetchInterval: 2000,
    retry: false,
  });
  const busy =
    operations.data?.some(
      (op) => op.state === "queued" || op.state === "running",
    ) ?? false;
  const detail = useQuery({
    queryKey: [...key, "detail", bindingId],
    queryFn: () => api.getComputerBindingDetail(bindingId, admin),
    enabled: !!userId && !!bindingId,
    refetchInterval: 2000,
    retry: false,
  });
  const refresh = () => client.invalidateQueries({ queryKey: key });
  const check = useMutation({
    mutationFn: () =>
      admin
        ? api.checkAdminLinuxUser(bindingId)
        : api.checkLinuxUser(bindingId),
    onSettled: refresh,
  });
  const discover = useMutation({
    mutationFn: () => api.discoverComputerBinding(bindingId),
    onSuccess: refresh,
  });
  const recover = useMutation({
    mutationFn: ({
      id,
      action,
    }: {
      id: string;
      action: "cancel" | "acknowledge";
    }) => api.recoverComputerOperation(bindingId, id, action),
    onSuccess: refresh,
  });
  const lifecycle = useMutation({
    gcTime: 0,
    mutationFn: (input: ComputerLifecycleInput) =>
      api.computerBindingLifecycle(bindingId, input),
    onSuccess: refresh,
  });
  return { detail, operations, busy, discover, recover, lifecycle, check };
}
