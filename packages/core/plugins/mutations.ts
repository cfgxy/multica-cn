import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { PluginConfigRequest, PluginInstallRequest, PluginPreviewRequest } from "../types";
import { pluginKeys } from "./queries";

function useInvalidatePlugins(wsId: string) {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: pluginKeys.all(wsId) });
}

/**
 * Preview is deliberately a mutation, not a query: it is the first half of a
 * consent flow the administrator started, so it must run on an explicit action
 * rather than be replayed by a cache refetch.
 */
export function usePreviewPlugin(wsId: string) {
  return useMutation({
    mutationFn: (request: PluginPreviewRequest) => api.previewPlugin(wsId, request),
  });
}

/**
 * Publishes an artifact bundle. A published version is immutable, so this only
 * ever adds one — it can never change what an installed workspace is running.
 */
export function usePublishPluginPackage(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (bundle: File) => api.publishPluginPackage(wsId, bundle),
    onSettled: invalidate,
  });
}

/** The development channel: publish from MULTICA_PLUGIN_DIR instead of a zip. */
export function usePublishLocalPluginPackage(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (name: string) => api.publishLocalPluginPackage(wsId, name),
    onSettled: invalidate,
  });
}

/**
 * Lists a package on the instance directory, or takes it off.
 *
 * Unlisting stops discovery and new installs only. Workspaces already running a
 * version keep running it, which is what makes unlisting a decision a publisher
 * can actually take.
 */
export function useSetPluginPackageVisibility(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: ({ packageId, isPublic }: { packageId: string; isPublic: boolean }) =>
      api.setPluginPackageVisibility(wsId, packageId, isPublic),
    onSettled: invalidate,
  });
}

/** Withdraws one published version from the directory, or restores it. */
export function useSetPluginVersionWithdrawn(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: ({ versionId, withdrawn }: { versionId: string; withdrawn: boolean }) =>
      api.setPluginVersionWithdrawn(wsId, versionId, withdrawn),
    onSettled: invalidate,
  });
}

export function useDeletePluginPackage(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (packageId: string) => api.deletePluginPackage(wsId, packageId),
    onSettled: invalidate,
  });
}

export function useInstallPlugin(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (request: PluginInstallRequest) => api.installPlugin(wsId, request),
    onSettled: invalidate,
  });
}

export function useConfigurePlugin(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: ({ installationId, ...request }: PluginConfigRequest & { installationId: string }) =>
      api.configurePlugin(wsId, installationId, request),
    onSettled: invalidate,
  });
}

export function useSetPluginEnabled(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: ({ installationId, enabled }: { installationId: string; enabled: boolean }) =>
      api.setPluginEnabled(wsId, installationId, enabled),
    onSettled: invalidate,
  });
}

/**
 * Pins the approved tool set for one `mcp` hook.
 *
 * `tools` is the complete set the administrator wants approved, never a delta —
 * unchecking one and saving is what revokes it. An empty array withdraws the
 * hook, and the next task claim stops offering it to agents.
 */
export function useApprovePluginMCPTools(wsId: string, installationId: string, hookKey: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (tools: string[]) => api.approvePluginMCPTools(wsId, installationId, hookKey, tools),
    onSettled: invalidate,
  });
}

/**
 * Removes one stored secret, leaving the installation in place.
 *
 * The cleanup lever that still works with plugins_v1 off, where configuring is
 * refused: without it, uninstalling the whole plugin would be the only way to
 * get a leaked credential out of the database.
 */
export function useClearPluginSecret(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: ({ installationId, key }: { installationId: string; key: string }) =>
      api.clearPluginSecret(wsId, installationId, key),
    onSettled: invalidate,
  });
}

export function useUninstallPlugin(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (installationId: string) => api.uninstallPlugin(wsId, installationId),
    onSettled: invalidate,
  });
}

/**
 * Invokes a hook the user asked for.
 *
 * A mutation rather than a query for the same reason preview is: it performs an
 * outbound call to a third-party server, and a cache refetch must never replay
 * it. Whatever the hook did on the far side is not something to repeat because
 * a component remounted.
 *
 * Deliberately does NOT invalidate on settle. A hook may have changed nothing,
 * or may have written through the Action API under its own attribution — the
 * caller knows which and invalidates what it actually expects to have moved.
 */
export function useInvokePluginHook() {
  return useMutation({
    mutationFn: ({ installationId, hookKey, ...request }: {
      installationId: string;
      hookKey: string;
      trigger: "ui" | "manual";
      issueId?: string;
      input?: unknown;
    }) => api.invokePluginHook(installationId, hookKey, request),
  });
}

export function useRotatePluginToken(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (installationId: string) => api.rotatePluginToken(wsId, installationId),
    onSettled: invalidate,
  });
}

export function useRevokePluginToken(wsId: string) {
  const invalidate = useInvalidatePlugins(wsId);
  return useMutation({
    mutationFn: (installationId: string) => api.revokePluginToken(wsId, installationId),
    onSettled: invalidate,
  });
}
