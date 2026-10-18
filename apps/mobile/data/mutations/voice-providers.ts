/**
 * Voice provider pool mutations (RUYI-749) — mobile mirror of
 * packages/core/voice/mutations.ts. Every write invalidates the pool list;
 * the credential PUT response IS the refreshed row (保存即探针 — the server
 * probes before responding), so there is nothing optimistic to reconcile.
 * Provider delete also clears workspace settings references server-side,
 * so the workspace list (settings bag) settles too.
 */
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/data/api";
import { voiceProvidersKeys } from "@/data/voice-providers";
import { useWorkspaceStore } from "@/data/workspace-store";

const WORKSPACES_KEY = ["workspaces"] as const;

export function useCreateVoiceProvider() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["createVoiceProvider", wsId] as const,
    mutationFn: (body: { display_name: string; provider_type: string }) => {
      if (!wsId) throw new Error("no workspace selected");
      return api.createVoiceProvider(wsId, body);
    },
    onSettled: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProvidersKeys.list(wsId) });
    },
  });
}

export function useUpdateVoiceProvider() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["updateVoiceProvider", wsId] as const,
    mutationFn: ({
      providerId,
      patch,
    }: {
      providerId: string;
      patch: { display_name?: string; enabled?: boolean };
    }) => {
      if (!wsId) throw new Error("no workspace selected");
      return api.updateVoiceProvider(wsId, providerId, patch);
    },
    onSettled: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProvidersKeys.list(wsId) });
    },
  });
}

export function useDeleteVoiceProvider() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["deleteVoiceProvider", wsId] as const,
    mutationFn: (providerId: string) => {
      if (!wsId) throw new Error("no workspace selected");
      return api.deleteVoiceProvider(wsId, providerId);
    },
    onSettled: () => {
      if (!wsId) return;
      void qc.invalidateQueries({ queryKey: voiceProvidersKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: WORKSPACES_KEY });
    },
  });
}

export function usePutVoiceProviderCredential() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["putVoiceProviderCredential", wsId] as const,
    mutationFn: ({
      providerId,
      credentialKey,
      value,
    }: {
      providerId: string;
      credentialKey: string;
      value: string;
    }) => {
      if (!wsId) throw new Error("no workspace selected");
      return api.putVoiceProviderCredential(wsId, providerId, credentialKey, value);
    },
    onSettled: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProvidersKeys.list(wsId) });
    },
  });
}

export function useDeleteVoiceProviderCredential() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["deleteVoiceProviderCredential", wsId] as const,
    mutationFn: ({
      providerId,
      credentialKey,
    }: {
      providerId: string;
      credentialKey: string;
    }) => {
      if (!wsId) throw new Error("no workspace selected");
      return api.deleteVoiceProviderCredential(wsId, providerId, credentialKey);
    },
    onSettled: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProvidersKeys.list(wsId) });
    },
  });
}

export function useProbeVoiceProviderCredential() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["probeVoiceProviderCredential", wsId] as const,
    mutationFn: (providerId: string) => {
      if (!wsId) throw new Error("no workspace selected");
      return api.probeVoiceProviderCredential(wsId, providerId);
    },
    onSettled: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProvidersKeys.list(wsId) });
    },
  });
}

/**
 * Whole-bag settings write — the PATCH replaces the stored bag, so callers
 * merge the current bag and send it whole. The workspace list is the cache
 * of record for the settings bag (RUYI-734), hence its invalidation.
 */
export function useUpdateWorkspaceSettings() {
  const qc = useQueryClient();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);

  return useMutation({
    mutationKey: ["updateWorkspaceSettings", wsId] as const,
    mutationFn: (settings: Record<string, unknown>) => {
      if (!wsId) throw new Error("no workspace selected");
      return api.updateWorkspaceSettings(wsId, settings);
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: WORKSPACES_KEY });
    },
  });
}
