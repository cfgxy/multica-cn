// RUYI-645: read-only node-ops channel hooks. Queries hang off the runtime's
// ops endpoints; the command list self-polls only while something is still
// pending/claimed (the daemon claims on its next heartbeat and the receipt
// lands one round trip later), so an idle panel costs nothing.
import { queryOptions } from "@tanstack/react-query";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  CreateRuntimeOpsCommandRequest,
  RuntimeOpsCommand,
} from "../types";

export const runtimeOpsKeys = {
  all: () => ["runtimes", "ops"] as const,
  inventory: (runtimeId: string) =>
    [...runtimeOpsKeys.all(), "inventory", runtimeId] as const,
  commands: (runtimeId: string) =>
    [...runtimeOpsKeys.all(), "commands", runtimeId] as const,
};

export function runtimeOpsInventoryOptions(runtimeId: string) {
  return queryOptions({
    queryKey: runtimeOpsKeys.inventory(runtimeId),
    queryFn: () => api.getRuntimeOpsInventory(runtimeId),
    enabled: !!runtimeId,
  });
}

export function runtimeOpsCommandsKey(runtimeId: string) {
  return runtimeOpsKeys.commands(runtimeId);
}

export function opsInFlight(commands: RuntimeOpsCommand[] | undefined): boolean {
  return (
    commands?.some(
      (command) =>
        command.status === "pending" || command.status === "claimed",
    ) ?? false
  );
}

// Poll cadence lives with the consumer: pass refetchIntervalOverdue at the
// useQuery call site as `refetchInterval: (q) => opsInFlight(q.state.data) ? … : false`.
export function runtimeOpsCommandsOptions(runtimeId: string) {
  return queryOptions({
    queryKey: runtimeOpsCommandsKey(runtimeId),
    queryFn: () => api.listRuntimeOpsCommands(runtimeId),
    enabled: !!runtimeId,
  });
}

export function useSubmitRuntimeOpsCommand(runtimeId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: CreateRuntimeOpsCommandRequest) =>
      api.submitRuntimeOpsCommand(runtimeId, payload),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeOpsKeys.commands(runtimeId) });
    },
  });
}
