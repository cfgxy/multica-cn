import { queryOptions } from "@tanstack/react-query";
import type { RuntimeModelsResult } from "@multica/core/types";
import { resolveRuntimeModels } from "@/lib/runtime-discovery";

// Freshness policy mirrors packages/core/runtimes/models.ts: a live discovery
// result is trusted for 5 minutes; a server-cache answer is stale immediately
// so the server's own serve window bounds observable staleness (MUL-5444).
const LIVE_MODELS_STALE_TIME_MS = 5 * 60_000;
const MODELS_GC_TIME_MS = 30 * 60_000;

export const runtimeModelsKeys = {
  all: () => ["runtime-models"] as const,
  forRuntime: (runtimeId: string) =>
    [...runtimeModelsKeys.all(), runtimeId] as const,
};

function staleTimeFor(data: RuntimeModelsResult | undefined): number {
  if (!data) return 0;
  return data.cached ? 0 : LIVE_MODELS_STALE_TIME_MS;
}

export function runtimeModelsOptions(runtimeId: string | null | undefined) {
  return queryOptions({
    queryKey: runtimeId
      ? runtimeModelsKeys.forRuntime(runtimeId)
      : runtimeModelsKeys.all(),
    queryFn: () => resolveRuntimeModels(runtimeId as string),
    enabled: Boolean(runtimeId),
    staleTime: (query) => staleTimeFor(query.state.data as RuntimeModelsResult | undefined),
    gcTime: MODELS_GC_TIME_MS,
    retry: false,
  });
}
