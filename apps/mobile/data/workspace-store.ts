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
  /** Server the confirmed id belongs to. restoreSlug compares it so a
   *  cross-server switch persisting the SAME slug string still clears the
   *  id (slug-only comparison would leak the old server's workspace). */
  currentWorkspaceServerId: string | null;
  /** Set the active workspace and persist the slug (id is in-memory only —
   *  it's resolved from the workspaces list query, not stored). */
  setCurrentWorkspace: (id: string, slug: string) => Promise<void>;
  /** Restore the slug from SecureStore on cold start / server switch. On
   *  cold start the id stays null until the workspaces list query resolves;
   *  on a mid-session replay (Android Activity recreation re-runs
   *  initialize()) the identity is unchanged, so the confirmed id is kept. */
  restoreSlug: () => Promise<string | null>;
  clear: () => Promise<void>;
}

export const useWorkspaceStore = create<WorkspaceState>((set, get) => ({
  currentWorkspaceId: null,
  currentWorkspaceSlug: null,
  currentWorkspaceServerId: null,

  setCurrentWorkspace: async (id, slug) => {
    const { activeServerId } = useServerStore.getState();
    if (get().currentWorkspaceSlug !== slug) {
      invalidateNewIssueSubmissionContext();
    }
    set({
      currentWorkspaceId: id,
      currentWorkspaceSlug: slug,
      currentWorkspaceServerId: activeServerId,
    });
    await setSlug(activeServerId, slug);
  },

  restoreSlug: async () => {
    const { activeServerId } = useServerStore.getState();
    const slug = await getSlug(activeServerId);
    // Identity is (server, slug): clear the confirmed id only when it actually
    // changed. Mid-session replays (Activity recreation re-runs initialize())
    // come back with the same identity — nulling the id there would re-key
    // every tab list query to `[..., null, ...]` with enabled:false, silently
    // emptying the UI with no self-heal, since the layout sync effect's deps
    // wouldn't change (RUYI-543). A server switch keeps the existing
    // semantics: a target server without a saved snapshot must NOT inherit
    // the previous server's in-memory workspace — otherwise the entry
    // redirect would route straight into the old server's workspace. The
    // serverId stamp also covers a new server that persisted the SAME slug
    // string: a different workspace that a slug-only check cannot detect.
    const prev = get();
    const identityChanged =
      prev.currentWorkspaceSlug !== slug ||
      prev.currentWorkspaceServerId !== activeServerId;
    if (identityChanged) {
      invalidateNewIssueSubmissionContext();
    }
    set({
      currentWorkspaceId: identityChanged ? null : prev.currentWorkspaceId,
      currentWorkspaceSlug: slug,
      currentWorkspaceServerId: activeServerId,
    });
    return slug;
  },

  clear: async () => {
    if (get().currentWorkspaceSlug !== null) {
      invalidateNewIssueSubmissionContext();
    }
    set({
      currentWorkspaceId: null,
      currentWorkspaceSlug: null,
      currentWorkspaceServerId: null,
    });
    const { activeServerId } = useServerStore.getState();
    await clearSlug(activeServerId);
  },
}));

/** Sync helper for ApiClient.fetch — reads the current slug without React. */
export function getCurrentSlug(): string | null {
  return useWorkspaceStore.getState().currentWorkspaceSlug;
}
