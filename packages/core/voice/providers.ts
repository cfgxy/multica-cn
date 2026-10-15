import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { VoiceProvider } from "../types";

/**
 * Voice provider pool queries (RUYI-693 D2, management UI RUYI-749).
 *
 * One pool per workspace. The listing is member-visible (the settings tab
 * renders for everyone); writes are workspace-admin state, enforced
 * server-side. No secret material is ever in the payload — only the coarse
 * credential_status badge and the last probe verdict, which is also what
 * makes the badge self-healing: a credential save re-probes server-side
 * (保存即探针) and the next refetch shows the fresh verdict, so there is
 * deliberately no client-side probe cache.
 */

export const voiceProviderKeys = {
  all: (wsId: string) => ["voice-providers", wsId] as const,
  list: (wsId: string) => [...voiceProviderKeys.all(wsId), "list"] as const,
};

export function voiceProviderListOptions(wsId: string) {
  return queryOptions({
    queryKey: voiceProviderKeys.list(wsId),
    queryFn: () => api.listVoiceProviders(wsId),
    enabled: !!wsId,
    staleTime: 30_000,
  });
}

export type { VoiceProvider };
export {
  parseWorkspaceVoiceSettings,
  voiceProviderChainEligible,
  parseVoiceAdvancedDraft,
} from "./chain";
