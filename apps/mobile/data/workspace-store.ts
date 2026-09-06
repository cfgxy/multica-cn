/**
 * Mobile workspace store — Zustand. Holds the active workspace (id + slug)
 * and persists the slug to SecureStore so cold starts restore the last
 * selection without re-prompting.
 *
 * The route is the source of truth for which workspace is active
 * (`/[workspace]/...` URL segment, set by the layout that reads
 * useLocalSearchParams). This store is a fast cache that ApiClient.fetch
 * reads synchronously to inject the X-Workspace-Slug header — touching
 * the router or React context on every fetch would be ugly. Routes
 * sync into the store on mount via setCurrentWorkspace.
 *
 * The slug persists under the active server's scoped key
 * (secure-storage.slugKeyFor) — part of the per-server session snapshot, so
 * switching servers restores each server's own last-used workspace.
 * Logic mirrors packages/core/platform/workspace-storage.ts, scoped per
 * server instead of one global value.
 */
import { create } from "zustand";

import { clearSlug, getSlug, setSlug } from "./secure-storage";
import { useServerStore } from "./server-store";
import { invalidateNewIssueSubmissionContext } from "./stores/new-issue-draft-store";

interface WorkspaceState {
  currentWorkspaceId: string | null;
  currentWorkspaceSlug: string | null;
  /** Set the active workspace and persist the slug (id is in-memory only —
   *  it's resolved from the workspaces list query, not stored). */
  setCurrentWorkspace: (id: string, slug: string) => Promise<void>;
  /** Restore the slug from SecureStore on cold start / server switch. id
   *  stays null until the workspaces list query resolves. */
  restoreSlug: () => Promise<string | null>;
  clear: () => Promise<void>;
}

export const useWorkspaceStore = create<WorkspaceState>((set, get) => ({
  currentWorkspaceId: null,
  currentWorkspaceSlug: null,

  setCurrentWorkspace: async (id, slug) => {
    if (get().currentWorkspaceSlug !== slug) {
      invalidateNewIssueSubmissionContext();
    }
    set({ currentWorkspaceId: id, currentWorkspaceSlug: slug });
    const { activeServerId } = useServerStore.getState();
    await setSlug(activeServerId, slug);
  },

  restoreSlug: async () => {
    const { activeServerId } = useServerStore.getState();
    const slug = await getSlug(activeServerId);
    // Unconditional overwrite (including null): restoreSlug also runs on
    // server switch, where a target server without a saved snapshot must
    // NOT inherit the previous server's in-memory slug — otherwise the
    // entry redirect would route straight into the old server's workspace.
    if (get().currentWorkspaceSlug !== slug) {
      invalidateNewIssueSubmissionContext();
    }
    set({ currentWorkspaceId: null, currentWorkspaceSlug: slug });
    return slug;
  },

  clear: async () => {
    if (get().currentWorkspaceSlug !== null) {
      invalidateNewIssueSubmissionContext();
    }
    set({ currentWorkspaceId: null, currentWorkspaceSlug: null });
    const { activeServerId } = useServerStore.getState();
    await clearSlug(activeServerId);
  },
}));

/** Sync helper for ApiClient.fetch — reads the current slug without React. */
export function getCurrentSlug(): string | null {
  return useWorkspaceStore.getState().currentWorkspaceSlug;
}
