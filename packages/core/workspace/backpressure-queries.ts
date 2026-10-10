import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { WorkspaceBackpressureSettingsSave } from "../types";

/**
 * Workspace host-backpressure settings keys (RUYI-618). The card is the
 * only reader of itself today, but the keys are namespaced so a future
 * consumer (e.g. a daemon status badge showing the active watermarks)
 * invalidates alongside it.
 */
export const backpressureSettingsKeys = {
  all: (wsId: string) => ["workspace-backpressure-settings", wsId] as const,
  root: (wsId: string) => backpressureSettingsKeys.all(wsId),
};

// Hand-rolled options object (no `queryOptions()` helper): tab-level tests
// mock @tanstack/react-query wholesale without that export, and this runs
// during render of the settings tab that hosts the card.
export function workspaceBackpressureSettingsOptions(wsId: string) {
  return {
    queryKey: backpressureSettingsKeys.root(wsId),
    queryFn: () => api.getWorkspaceBackpressureSettings(),
    enabled: wsId !== "",
  };
}

/** Save the card (owner-only server-side). Validation failures (422) surface
 * through the mutation error so the form can show the hysteresis reason. */
export function useSaveWorkspaceBackpressureSettings(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (save: WorkspaceBackpressureSettingsSave) =>
      api.putWorkspaceBackpressureSettings(save),
    onSuccess: () => qc.invalidateQueries({ queryKey: backpressureSettingsKeys.all(wsId) }),
  });
}
