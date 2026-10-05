import { queryOptions } from "@tanstack/react-query";
import { resolveRuntimeLocalSkills } from "@/lib/runtime-discovery";

// One daemon round trip = the runtime capability snapshot (local skills +
// redacted MCP inventory). Same shape and staleness as the web
// runtimeCapabilitiesOptions alias.
export const runtimeCapabilityKeys = {
  all: () => ["runtime-capabilities"] as const,
  forRuntime: (runtimeId: string) =>
    [...runtimeCapabilityKeys.all(), runtimeId] as const,
};

export function runtimeCapabilitiesOptions(
  runtimeId: string | null | undefined,
) {
  return queryOptions({
    queryKey: runtimeId
      ? runtimeCapabilityKeys.forRuntime(runtimeId)
      : runtimeCapabilityKeys.all(),
    queryFn: () => resolveRuntimeLocalSkills(runtimeId as string),
    enabled: Boolean(runtimeId),
    staleTime: 30_000,
    retry: false,
  });
}
