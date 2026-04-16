import { useEffect } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { workspaceBySlugOptions } from "@multica/core/workspace";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useAuthStore } from "@multica/core/auth";
import { NoAccessPage } from "@multica/views/workspace/no-access-page";
import { useWorkspaceSeen } from "@multica/views/workspace/use-workspace-seen";

/**
 * Desktop equivalent of apps/web/app/[workspaceSlug]/layout.tsx.
 *
 * Resolves the URL slug → workspace UUID via the React Query list cache
 * (seeded by AuthInitializer). Children do not render until the workspace
 * is fully resolved — useWorkspaceId() inside child pages is therefore
 * guaranteed non-null when called. Two industry-standard identities are
 * kept distinct: slug (URL / browser) and UUID (API / cache keys).
 *
 * If the slug doesn't resolve to any workspace the user has access to,
 * we render NoAccessPage instead of silently redirecting — users get
 * explicit feedback for stale bookmarks or revoked access.
 */
export function WorkspaceRouteLayout() {
  const { workspaceSlug } = useParams<{ workspaceSlug: string }>();
  const user = useAuthStore((s) => s.user);
  const isAuthLoading = useAuthStore((s) => s.isLoading);

  const { data: workspace, isFetched: listFetched } = useQuery({
    ...workspaceBySlugOptions(workspaceSlug ?? ""),
    enabled: !!user && !!workspaceSlug,
  });

  // Feed the URL slug into the platform singleton so the API client's
  // X-Workspace-Slug header and persist namespace follow the active tab.
  // setCurrentWorkspace self-dedupes on slug equality — safe to call on
  // every render (matters on desktop, where N tabs each mount their own
  // layout). Rehydrate is the singleton's internal side effect.
  if (workspace && workspaceSlug) {
    setCurrentWorkspace(workspaceSlug, workspace.id);
  }

  // Double-write legacy localStorage key for rollback compatibility — a
  // pre-refactor build reads it to pick the initial workspace. Placed in
  // an effect so repeated renders don't hammer localStorage.
  useEffect(() => {
    if (!workspace) return;
    try {
      localStorage.setItem("multica_workspace_id", workspace.id);
    } catch {
      // non-critical
    }
  }, [workspace]);

  // Remember whether this slug has resolved before (see hook docs). Gates
  // the NoAccessPage render below so active workspace removal doesn't
  // flash "Workspace not available" before the navigate lands.
  const hasBeenSeen = useWorkspaceSeen(workspaceSlug, !!workspace);

  if (isAuthLoading) return null;
  if (!workspaceSlug) return null;
  // Don't render children until workspace is resolved. useWorkspaceId()
  // throws when the workspace list hasn't populated or the slug is
  // unknown — gating here is the single point where that invariant is
  // enforced, so every descendant can call useWorkspaceId() safely.
  if (!listFetched) return null;
  if (!workspace) {
    // Active workspace just removed (delete/leave/realtime eviction) —
    // navigate is in flight; hold null briefly instead of flashing
    // NoAccessPage.
    if (hasBeenSeen) return null;
    // Genuinely inaccessible slug (stale bookmark, revoked access, or a
    // link from a former teammate's workspace) → explicit feedback.
    return <NoAccessPage />;
  }

  return (
    <WorkspaceSlugProvider slug={workspaceSlug}>
      <Outlet />
    </WorkspaceSlugProvider>
  );
}
