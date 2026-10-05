/**
 * Runtime capability discovery query (RUYI-418 B3) — the local runtime's
 * read-only inventory of inherited skills and MCP servers, surfaced on the
 * agent skills / MCP screens. Mirrors packages/core/runtimes/local-skills.ts:
 * same cache key shape, 30s freshness, and no retry (a failed discovery
 * round-trip stays failed until the user explicitly refreshes).
 */
import { queryOptions } from "@tanstack/react-query";
import { resolveRuntimeLocalSkills } from "@/lib/runtime-discovery";

export const runtimeLocalSkillsKeys = {
  all: () => ["runtimes", "local-skills"] as const,
  forRuntime: (runtimeId: string) =>
    [...runtimeLocalSkillsKeys.all(), runtimeId] as const,
};

// Capability-oriented alias, same as the core export: agent detail surfaces
// read skills + MCP inventory from one discovery round trip.
export const runtimeCapabilitiesOptions = (runtimeId: string | null | undefined) =>
  queryOptions({
    queryKey: runtimeId
      ? runtimeLocalSkillsKeys.forRuntime(runtimeId)
      : runtimeLocalSkillsKeys.all(),
    queryFn: () => resolveRuntimeLocalSkills(runtimeId as string),
    enabled: Boolean(runtimeId),
    staleTime: 30_000,
    retry: false,
  });
