import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { voiceProviderKeys } from "./providers";

/**
 * Voice provider pool mutations (RUYI-749). Every mutation invalidates the
 * pool list on success; the delete also invalidates the workspace list,
 * because delete nulls the workspace settings' voice references server-side
 * and the availability gate derives from that bag.
 *
 * Deliberately NOT optimistic: the settings tab is a form surface, and the
 * credential PUT's response IS the refreshed row (保存即探针) — the badge
 * must show the probe outcome the server recorded, not a guess.
 */

export function useCreateVoiceProvider() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: { display_name: string; provider_type: string }) =>
      api.createVoiceProvider(wsId, data),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProviderKeys.all(wsId) });
    },
  });
}

export function useUpdateVoiceProvider() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      providerId,
      data,
    }: {
      providerId: string;
      data: { display_name?: string; enabled?: boolean };
    }) => api.updateVoiceProvider(wsId, providerId, data),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProviderKeys.all(wsId) });
    },
  });
}

export function useDeleteVoiceProvider() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (providerId: string) => api.deleteVoiceProvider(wsId, providerId),
    onSuccess: () => {
      if (!wsId) return;
      void qc.invalidateQueries({ queryKey: voiceProviderKeys.all(wsId) });
      // The server nulled workspace settings' voice references to the
      // deleted provider inside the delete transaction.
      void qc.invalidateQueries({ queryKey: ["workspaces"] });
    },
  });
}

export function usePutVoiceProviderCredential() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({
      providerId,
      credentialKey,
      value,
    }: {
      providerId: string;
      credentialKey: string;
      value: string;
    }) => api.putVoiceProviderCredential(wsId, providerId, credentialKey, value),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProviderKeys.all(wsId) });
    },
  });
}

export function useDeleteVoiceProviderCredential() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ providerId, credentialKey }: { providerId: string; credentialKey: string }) =>
      api.deleteVoiceProviderCredential(wsId, providerId, credentialKey),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProviderKeys.all(wsId) });
    },
  });
}

export function useProbeVoiceProviderCredential() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (providerId: string) => api.probeVoiceProviderCredential(wsId, providerId),
    onSuccess: () => {
      if (wsId) void qc.invalidateQueries({ queryKey: voiceProviderKeys.all(wsId) });
    },
  });
}
