import { useEffect, useRef } from "react";
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { Computer } from "./schema";
import type { AdminComputer, AdminComputerRuntime } from "./admin-schema";

export function useInstanceAccess(userId: string) {
  return useQuery({
    queryKey: ["instance-access", userId],
    enabled: !!userId,
    queryFn: () => api.getInstanceAccess(),
    retry: false,
    staleTime: 0,
  });
}
export function useComputerAdmin(userId: string, allowed: boolean) {
  const client = useQueryClient();
  const key = ["computer-admin", userId];
  const enabled = !!userId && allowed;
  const computers = useQuery({
    queryKey: [...key, "computers"],
    enabled,
    queryFn: () => api.listAdminComputers(),
    retry: false,
  });
  const bindings = useQuery({
    queryKey: [...key, "bindings"],
    enabled,
    queryFn: () => api.listAdminComputerBindings(),
    retry: false,
  });
  const audit = useQuery({
    queryKey: [...key, "audit"],
    enabled,
    queryFn: () => api.listComputerAudit(),
    retry: false,
  });
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: key }),
      client.invalidateQueries({ queryKey: ["computers"] }),
    ]);
  };
  const register = useMutation({
    mutationFn: (input: Omit<Computer, "id" | "enabled">) => api.registerComputer(input),
    onSuccess: refresh,
  });
  const update = useMutation({
    mutationFn: ({
      id,
      ...input
    }: Partial<Omit<AdminComputer, "id">> & { id: string }) =>
      api.updateAdminComputer(id, input),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deleteAdminComputer(id),
    onSuccess: refresh,
  });
  // Checking a draft does not touch the registry, so it must not invalidate.
  const checkDraft = useMutation({
    mutationFn: (input: Omit<Computer, "id" | "enabled">) => api.checkAdminComputerDraft(input),
  });
  // Checking a registered Computer records the verdict, so the list refreshes.
  const check = useMutation({
    mutationFn: (id: string) => api.checkAdminComputer(id),
    onSuccess: refresh,
  });
  const checkLinuxUser = useMutation({
    mutationFn: (id: string) => api.checkAdminLinuxUser(id),
    onSettled: () => client.invalidateQueries({ queryKey: [...key, "audit"] }),
  });
  return { computers, bindings, audit, register, update, remove, check, checkDraft, checkLinuxUser, refresh };
}

export function useComputerBindingRuntimes(
  userId: string,
  bindingId: string,
  enabled = true,
  busy = false,
) {
  const isInstalling =
    useIsMutating({
      mutationKey: ["binding-runtime-install", userId, bindingId],
    }) > 0;
  const runtimes = useQuery<AdminComputerRuntime[]>({
    queryKey: ["computers", userId, "runtimes", bindingId],
    enabled: enabled && !!userId && !!bindingId,
    queryFn: ({ signal }) => api.listComputerBindingRuntimes(bindingId, signal),
    staleTime: 30_000,
    refetchInterval: (query) =>
      busy ||
      query.state.data?.some(
        (runtime) => runtime.latest_version_state === "checking",
      )
        ? 2000
        : 60_000,
    retry: false,
  });
  const wasBusy = useRef(busy);
  const { refetch } = runtimes;
  useEffect(() => {
    if (wasBusy.current && !busy) void refetch();
    wasBusy.current = busy;
  }, [busy, refetch]);
  return { runtimes, isInstalling };
}

export function useComputerBindingRuntimeInstall(userId: string, bindingId: string, runtimeId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationKey: ["binding-runtime-install", userId, bindingId, runtimeId],
    mutationFn: (version: string) =>
      api.installComputerBindingRuntime(bindingId, runtimeId, version),
    onSettled: async () => {
      await client.invalidateQueries({ queryKey: ["computers", userId] });
      await client.invalidateQueries({ queryKey: ["computer-admin", userId, "audit"] });
    },
  });
}
