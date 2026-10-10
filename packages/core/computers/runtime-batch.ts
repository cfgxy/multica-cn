import {
  useMutation,
  useMutationState,
  useQueryClient,
} from "@tanstack/react-query";
import { api, ApiError } from "../api";
import type { AdminComputerRuntime } from "./admin-schema";

export type RuntimeInstallItem = { id: string; version: string; skip: boolean };
export type RuntimeBatchStatus =
  | "waiting"
  | "installing"
  | "succeeded"
  | "failed"
  | "skipped"
  | "stopped";
type BatchInput = { id: number; items: RuntimeInstallItem[] };
type ItemResult = {
  status: "succeeded" | "failed" | "skipped";
  error?: string;
};

export function runtimeInstallItem(
  runtime: AdminComputerRuntime,
): RuntimeInstallItem {
  const latestReady =
    runtime.latest_version_state === "ready" && !!runtime.latest_version;
  return {
    id: runtime.id,
    version:
      (runtime.supports_version ?? runtime.version_required) && latestReady
        ? runtime.latest_version
        : "latest",
    skip:
      !!runtime.installed_version &&
      runtime.probe_state !== "missing" &&
      latestReady &&
      runtime.update_available === false &&
      !runtime.probe_error,
  };
}

const delay = () => new Promise<void>((resolve) => setTimeout(resolve, 2000));

// Only terminal operations release the server's per-user installation lock.
// Unknown submission/completion outcomes stop the queue to avoid overlapping jobs.
export async function installRuntimeAndWait(
  bindingId: string,
  item: RuntimeInstallItem,
): Promise<ItemResult> {
  if (item.skip) return { status: "skipped" };
  let receipt;
  try {
    receipt = await api.installComputerBindingRuntime(
      bindingId,
      item.id,
      item.version,
    );
  } catch (error) {
    if (
      error instanceof ApiError &&
      [400, 404, 422, 503].includes(error.status)
    ) {
      return { status: "failed", error: error.message };
    }
    throw error;
  }
  const deadline = Date.now() + 21 * 60_000;
  let failures = 0;
  while (Date.now() < deadline) {
    let operations;
    try {
      operations = await api.listComputerOperations(bindingId);
      failures = 0;
    } catch (error) {
      if (++failures >= 3) throw error;
      await delay();
      continue;
    }
    const operation = operations.find(
      (entry) => entry.id === receipt.operation_id,
    );
    if (!operation) throw new Error("Installation operation is unavailable");
    if (operation.state === "succeeded") return { status: "succeeded" };
    if (["failed", "cancelled"].includes(operation.state)) {
      return {
        status: "failed",
        error: operation.error_summary || operation.state,
      };
    }
    if (operation.state === "interrupted") {
      throw new Error(
        "Installation was interrupted; check operation history before retrying",
      );
    }
    await delay();
  }
  throw new Error(
    "Installation status timed out; check operation history before retrying",
  );
}

export function useComputerBindingRuntimeBatch(
  userId: string,
  bindingId: string,
) {
  const client = useQueryClient();
  const key = ["binding-runtime-install", userId, bindingId, "batch"];
  const itemKey = [...key, "item"];
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ["computers", userId] }),
      client.invalidateQueries({
        queryKey: ["computer-admin", userId, "audit"],
      }),
    ]);
  };
  const batch = useMutation({
    mutationKey: key,
    retry: false,
    gcTime: 3 * 60 * 60_000,
    mutationFn: async (input: BatchInput) => {
      for (const item of input.items) {
        const mutation = client
          .getMutationCache()
          .build<
            ItemResult,
            Error,
            { batchId: number; item: RuntimeInstallItem },
            unknown
          >(client, {
            mutationKey: itemKey,
            retry: false,
            gcTime: 3 * 60 * 60_000,
            mutationFn: ({ item }) => installRuntimeAndWait(bindingId, item),
          });
        await mutation.execute({ batchId: input.id, item });
        // A failed installer returns a result, so subsequent items still run.
        void refresh();
      }
    },
    onSettled: refresh,
  });
  const batches = useMutationState({
    filters: {
      mutationKey: key,
      exact: true,
      predicate: (mutation) => mutation.state.variables !== undefined,
    },
    select: (mutation) => ({
      input: mutation.state.variables as BatchInput,
      status: mutation.state.status,
    }),
  });
  const latest = batches.at(-1);
  const entries = useMutationState({
    filters: {
      mutationKey: itemKey,
      exact: true,
      predicate: (mutation) => mutation.state.variables !== undefined,
    },
    select: (mutation) => ({
      input: mutation.state.variables as {
        batchId: number;
        item: RuntimeInstallItem;
      },
      status: mutation.state.status,
      result: mutation.state.data as ItemResult | undefined,
    }),
  });
  const results = new Map<string, RuntimeBatchStatus>();
  if (latest) {
    for (const item of latest.input.items) {
      results.set(item.id, latest.status === "error" ? "stopped" : "waiting");
    }
    for (const entry of entries) {
      if (entry.input.batchId !== latest.input.id) continue;
      results.set(
        entry.input.item.id,
        entry.status === "pending"
          ? "installing"
          : entry.status === "error"
            ? "stopped"
            : (entry.result?.status ?? "waiting"),
      );
    }
  }
  return {
    results,
    isPending: latest?.status === "pending",
    stopped: latest?.status === "error",
    start: (runtimes: AdminComputerRuntime[]) => {
      if (
        !runtimes.length ||
        client.isMutating({
          mutationKey: ["binding-runtime-install", userId, bindingId],
        })
      )
        return;
      batch.mutate({ id: Date.now(), items: runtimes.map(runtimeInstallItem) });
    },
  };
}
