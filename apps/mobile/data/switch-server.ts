/**
 * Server-session orchestration shared by the Settings picker and notification
 * bridge. Callers own their destination route; this module returns only the
 * resulting session state.
 */
import type { QueryClient } from "@tanstack/react-query";

import { api } from "./api";
import { useAuthStore } from "./auth-store";
import { useServerStore } from "./server-store";
import { getToken } from "./secure-storage";
import { useWorkspaceStore } from "./workspace-store";

export type ServerSwitchOutcome =
  /** Persisting the selected server failed before the active session changed. */
  | { kind: "failed"; error: unknown }
  /** Restoring the previous server after a failed switch also failed. */
  | { kind: "rollback-failed"; error: unknown }
  /** The server was removed after the confirmation dialog opened. */
  | { kind: "unavailable" }
  /** The selected server has no valid restorable session. */
  | { kind: "signed-out" }
  /** The selected server restored a session and its last workspace slug. */
  | {
      kind: "signed-in";
      slug: string | null;
      previousServerId: string;
    };

async function restorePreviousServer(
  serverId: string,
  queryClient: QueryClient,
): Promise<unknown | null> {
  try {
    await useServerStore.getState().setActiveServer(serverId);
    if (useServerStore.getState().activeServerId !== serverId) {
      throw new Error("The previous server is no longer available.");
    }
    api.setToken(null);
    queryClient.clear();
    await useAuthStore.getState().initialize();
    if (!useAuthStore.getState().user?.id) {
      throw new Error("Could not restore the previous server session.");
    }
    return null;
  } catch (error) {
    console.warn("[servers] failed to restore the previous server session", error);
    return error;
  }
}

/**
 * Select a server and restore its scoped session. A restorable token that
 * cannot rebuild a user is a transient restore failure, not a signed-out
 * state: restore the previous server so the caller can leave the user put.
 */
export async function switchServer(
  serverId: string,
  queryClient: QueryClient,
): Promise<ServerSwitchOutcome> {
  if (useAuthStore.getState().isServerSwitching) {
    return {
      kind: "failed",
      error: new Error("A server switch is already in progress."),
    };
  }

  const previousServerId = useServerStore.getState().activeServerId;
  let switched = false;
  useAuthStore.getState().setServerSwitching(true);

  const failAfterSwitch = async (
    error: unknown,
  ): Promise<ServerSwitchOutcome> => {
    if (!switched) return { kind: "failed", error };
    const rollbackError = await restorePreviousServer(previousServerId, queryClient);
    return rollbackError
      ? { kind: "rollback-failed", error: rollbackError }
      : { kind: "failed", error };
  };

  try {
    await useServerStore.getState().setActiveServer(serverId);
    if (useServerStore.getState().activeServerId !== serverId) {
      return { kind: "unavailable" };
    }
    switched = previousServerId !== serverId;
    // The base URL now points to the selected server. Old credentials and
    // cached data must not survive into that new server identity.
    api.setToken(null);
    queryClient.clear();
    await useAuthStore.getState().initialize();
    const { user } = useAuthStore.getState();
    if (user?.id) {
      return {
        kind: "signed-in",
        slug: useWorkspaceStore.getState().currentWorkspaceSlug,
        previousServerId,
      };
    }

    // Auth initialization clears a rejected (401) token, but deliberately
    // retains it for network and 5xx failures. That distinction lets callers
    // show a retryable failure instead of incorrectly navigating to login.
    if (await getToken(serverId)) {
      const error = new Error("Could not restore the selected server session.");
      return failAfterSwitch(error);
    }
    return { kind: "signed-out" };
  } catch (error) {
    return failAfterSwitch(error);
  } finally {
    useAuthStore.getState().setServerSwitching(false);
  }
}
