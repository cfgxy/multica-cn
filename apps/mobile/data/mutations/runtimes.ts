import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/data/api";
import { runtimeKeys, runtimeProfileKeys } from "@/data/queries/runtimes";

/**
 * RUYI-425 §4.3 mobile mutations for voice runtime instances and profiles —
 * mirror of packages/core/runtimes/mutations.ts + profiles.ts, pointed at
 * the mobile ApiClient so the X-Workspace-Slug header follows the mobile
 * workspace store. Invalidation keys match the desktop semantics: instance
 * writes refresh `["runtimes", wsId]` (list + every voice page), profile
 * writes refresh the profile catalog and the instance list (a new profile
 * changes what the create form offers; a profile change can relabel bound
 * instances).
 */

// §4.3: registers a manual voice instance; the list refresh makes the new
// instance's row (and every agent picker's voice slot) appear immediately.
export function useCreateManualRuntime(wsId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      name: string;
      profile_id: string;
      model?: string;
      advanced?: Record<string, unknown>;
    }) => api.createManualRuntime(body),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

// §4.3: voice instance settings edits (custom_name / model / advanced /
// disabled) via the runtime PATCH extension.
export function useUpdateRuntime(wsId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      runtimeId,
      patch,
    }: {
      runtimeId: string;
      patch: {
        visibility?: "private" | "public";
        custom_name?: string;
        apply_to_machine?: boolean;
        model?: string;
        advanced?: Record<string, unknown>;
        disabled?: boolean;
      };
    }) => api.updateRuntime(runtimeId, patch),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

// §4.5: stores or rotates a credential; the server runs the §4.3 probe in
// the same request. The plaintext lives only in the mutation call — the
// response is the value-free badge plus the probe outcome, and the
// invalidation recomputes credential_status badges everywhere.
export function usePutRuntimeCredential(wsId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      runtimeId,
      credentialKey,
      value,
    }: {
      runtimeId: string;
      credentialKey: string;
      value: string;
    }) => api.putRuntimeCredential(runtimeId, credentialKey, value),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

// §4.5: removes the stored credential (badge falls back to not_configured).
// Server-side idempotent.
export function useDeleteRuntimeCredential(wsId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      runtimeId,
      credentialKey,
    }: {
      runtimeId: string;
      credentialKey: string;
    }) => api.deleteRuntimeCredential(runtimeId, credentialKey),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

// RUYI-566: direct instance delete — only valid for profile-less
// instances; the server refuses (409) the profile-backed ones. Invalidates
// the runtime list so the deleted row disappears everywhere.
export function useDeleteRuntime(wsId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (runtimeId: string) => api.deleteRuntime(runtimeId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

// RUYI-566: the supported delete channel for profile-backed (manual voice)
// instances — the server's single-transaction cascade removes the bound
// instances, their stored credentials and the profile. Invalidation matches
// @multica/core/runtimes/profiles: the profile catalog plus the instance
// list (the instances vanish with it).
export function useDeleteRuntimeProfile(wsId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (profileId: string) => api.deleteRuntimeProfile(wsId ?? "", profileId),
    onSettled: () => {
      if (wsId) {
        qc.invalidateQueries({ queryKey: runtimeProfileKeys.all(wsId) });
        qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      }
    },
  });
}

// §4.3: the no-profile escape hatch — creates the workspace's default
// "Gemini Live" profile, then refreshes the catalog (the form selects the
// new profile id from the mutation result) and the instance list.
export function useCreateRuntimeProfile(wsId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      display_name: string;
      protocol_family: "gemini_live";
      command_name: "";
    }) => api.createRuntimeProfile(wsId ?? "", body),
    onSettled: () => {
      if (wsId) {
        qc.invalidateQueries({ queryKey: runtimeProfileKeys.all(wsId) });
        qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      }
    },
  });
}
