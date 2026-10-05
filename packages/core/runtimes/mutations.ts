import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { runtimeKeys } from "./queries";
import { workspaceKeys } from "../workspace/queries";
import { agentTaskSnapshotKeys } from "../agents/queries";

export function useDeleteRuntime(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (runtimeId: string) => api.deleteRuntime(runtimeId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

// Confirmed-delete counterpart to useDeleteRuntime. The dialog routes here when
// the strict DELETE refused with `runtime_has_active_agents` (or when the
// caller already knows the runtime has active agents and wants to skip the
// pre-flight refusal). Mutation fn returns the server-reported counts so
// the caller can render a richer success toast.
//
// Invalidates runtimes (the list / detail), workspace agents (they are unbound,
// so their runtime column and readiness change) and the agent presence snapshot
// (the delete also cancels queued/running tasks). Without the agent-side
// invalidation the Agents page would keep showing them as runnable.
export function useUnbindAgentsAndDeleteRuntime(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      runtimeId,
      expectedActiveAgentIds,
    }: {
      runtimeId: string;
      expectedActiveAgentIds: string[];
    }) => api.unbindAgentsAndDeleteRuntime(runtimeId, expectedActiveAgentIds),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.all(wsId) });
    },
  });
}

// RUYI-425 §4.5 (stage 2): stores or rotates a runtime instance credential.
// The plaintext value lives only in the mutation call — react-query never
// caches the request input into the query cache, and the server response is
// the value-free badge plus the connectivity-probe outcome. Invalidates the
// runtime list so credential_status badges recompute everywhere.
export function usePutRuntimeCredential(wsId: string) {
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

// RUYI-425 §4.5 (stage 2): removes the stored credential (badge falls back to
// not_configured). Server-side idempotent.
export function useDeleteRuntimeCredential(wsId: string) {
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

// RUYI-425 §4.3 (stage 3): registers a manual voice instance. Refreshes the
// runtime list so the new instance's machine card appears immediately.
export function useCreateManualRuntime(wsId: string) {
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

// useUpdateRuntime patches editable fields on a runtime (visibility, custom
// name). Invalidates the runtime list so the picker disabled-state and
// display names recompute.
export function useUpdateRuntime(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      runtimeId,
      patch,
    }: {
      runtimeId: string;
      patch: {
        visibility?: "private" | "public";
        // Empty string clears the custom name; omit to leave unchanged.
        custom_name?: string;
        apply_to_machine?: boolean;
        // RUYI-425 §4.3 voice instance settings (manual instances only).
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
