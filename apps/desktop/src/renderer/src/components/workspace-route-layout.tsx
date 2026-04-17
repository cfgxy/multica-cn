import { useEffect } from "react";
import { Outlet, useNavigate, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { WorkspaceSlugProvider, paths } from "@multica/core/paths";
import { workspaceBySlugOptions } from "@multica/core/workspace";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useAuthStore } from "@multica/core/auth";
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
 * Unlike web, desktop never renders a "workspace not available" page: the
 * app has no URL bar and no clickable links from outside the session, so
 * landing on an inaccessible slug can only mean stale state (persisted tab
 * from a previous account) or active eviction (admin removal, realtime
 * delete). Both cases resolve by bouncing to `/`, where IndexRedirect
 * picks a valid destination — the next workspace, or the new-workspace
 * overlay if the user has none.
 */
export function WorkspaceRouteLayout() {
  const { workspaceSlug } = useParams<{ workspaceSlug: string }>();
  const navigate = useNavigate();
  const user = useAuthStore((s) => s.user);
  const isAuthLoading = useAuthStore((s) => s.isLoading);

  // Workspace routes require auth. If user is unauthenticated (token
  // expired, logged out from another tab, etc.), bounce to /login.
  // Without this, the layout renders null and the user sees a blank page
  // stuck on /{slug}/...
  useEffect(() => {
    if (!isAuthLoading && !user) navigate(paths.login(), { replace: true });
  }, [isAuthLoading, user, navigate]);

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

  // Remember whether this slug has resolved before. `useWorkspaceSeen`
  // gates the auto-heal below so the mid-flight frame of an active-removal
  // navigation doesn't double-bounce.
  const hasBeenSeen = useWorkspaceSeen(workspaceSlug, !!workspace);

  // Stale slug (tab persisted from a previous account, or revoked access
  // that hasn't yet been cleaned up by validateWorkspaceSlugs): auto-heal
  // to `/`. IndexRedirect takes it from there.
  useEffect(() => {
    if (!user) return;
    if (!listFetched) return;
    if (workspace) return;
    if (hasBeenSeen) return; // active eviction in flight — let the other path win
    navigate("/", { replace: true });
  }, [user, listFetched, workspace, hasBeenSeen, navigate]);

  if (isAuthLoading) return null;
  if (!workspaceSlug) return null;
  // Don't render children until workspace is resolved. useWorkspaceId()
  // throws when the workspace list hasn't populated or the slug is
  // unknown — gating here is the single point where that invariant is
  // enforced, so every descendant can call useWorkspaceId() safely.
  if (!listFetched) return null;
  if (!workspace) return null; // auto-heal effect above handles the navigation

  return (
    <WorkspaceSlugProvider slug={workspaceSlug}>
      <Outlet />
    </WorkspaceSlugProvider>
  );
}
