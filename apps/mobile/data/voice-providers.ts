/**
 * Voice provider pool — mobile data layer (RUYI-694 D3). The D2 pool list
 * is member-visible (`GET /api/workspaces/{id}/voice-providers`), and the
 * availability gate derives from it plus the two chain levels:
 * `agent.voice_provider_id` (additive D2 field the core Agent type doesn't
 * carry yet — read defensively) and `workspace.settings.voice.default`.
 *
 * 改参重新首次: an admin credential save re-probes server-side (保存即探
 * 针) and flips the badge in the pool payload; this query refetches on
 * staleness/window focus, so the gate re-derives without any client-side
 * verdict cache.
 */
import { queryOptions, useQuery } from "@tanstack/react-query";
import type { Agent, Workspace } from "@multica/core/types";
import { workspaceBySlugOptions } from "@multica/core/workspace";
import type { VoiceAvailabilityOutcome } from "@/lib/voice/availability";
import { resolveVoiceAvailability } from "@/lib/voice/availability";
import { api, type VoiceProvider } from "./api";
import { useWorkspaceStore } from "./workspace-store";

export type { VoiceProvider };

export const voiceProvidersKeys = {
  list: (wsId: string) => ["workspaces", wsId, "voice-providers"] as const,
};

export function voiceProvidersOptions(wsId: string) {
  return queryOptions({
    queryKey: voiceProvidersKeys.list(wsId),
    queryFn: () => api.listVoiceProviders(wsId),
    enabled: !!wsId,
    staleTime: 30_000,
  });
}

/** D2 agent-level override triple, read off the additive server fields. */
export type AgentWithVoiceOverride = Agent & {
  voice_provider_id?: string | null;
  voice_model?: string | null;
};

/** The slice of workspace.settings the voice chain reads. */
function workspaceDefaultProviderId(
  workspace: Workspace | null,
): string | null {
  const settings = workspace?.settings as
    | { voice?: { default?: { provider_id?: string } } }
    | null
    | undefined;
  return settings?.voice?.default?.provider_id ?? null;
}

export interface VoiceAvailability {
  /** Null while the chain inputs are still loading — callers hide the entry. */
  availability: VoiceAvailabilityOutcome | null;
  resolving: boolean;
}

/**
 * Derives the voice entry gate for one agent in the current workspace.
 * Runs off the cached workspace list (settings bag) and the provider pool;
 * a null `availability` means inputs are still resolving, distinct from a
 * resolved 回退 verdict.
 */
export function useVoiceAvailability(
  agent: AgentWithVoiceOverride | null,
  wsSlug: string | null | undefined,
): VoiceAvailability {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: workspace, isLoading: workspaceLoading } = useQuery({
    ...workspaceBySlugOptions(wsSlug ?? ""),
    enabled: !!wsSlug,
  });
  const { data: providers = [], isLoading: providersLoading } = useQuery({
    ...voiceProvidersOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  const resolving =
    !agent || workspaceLoading || (providersLoading && !!wsId);
  if (resolving) return { availability: null, resolving: true };

  const providerId =
    agent.voice_provider_id || workspaceDefaultProviderId(workspace ?? null);
  const provider = providerId
    ? (providers.find((p) => p.id === providerId) ?? null)
    : null;

  return {
    availability: resolveVoiceAvailability({
      agentVoiceProviderId: agent.voice_provider_id,
      workspaceDefaultProviderId: workspaceDefaultProviderId(workspace ?? null),
      provider,
    }),
    resolving: false,
  };
}
