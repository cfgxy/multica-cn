"use client";

import { use, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { workspaceBySlugOptions } from "@multica/core/workspace";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useAuthStore } from "@multica/core/auth";
import { NoAccessPage } from "@multica/views/workspace/no-access-page";
import { useWorkspaceSeen } from "@multica/views/workspace/use-workspace-seen";

export default function WorkspaceLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ workspaceSlug: string }>;
}) {
  const { workspaceSlug } = use(params);
  const user = useAuthStore((s) => s.user);
  const isAuthLoading = useAuthStore((s) => s.isLoading);

  // Resolve workspace by slug from the React Query list cache.
  // Enabled only when user is authenticated — otherwise the list query isn't seeded.
  const { data: workspace, isFetched: listFetched } = useQuery({
    ...workspaceBySlugOptions(workspaceSlug),
    enabled: !!user,
  });

  // Render-phase sync: feed the URL slug into the platform singleton so
  // the first child query's X-Workspace-Slug header is already correct.
  // setCurrentWorkspace self-dedupes + runs rehydrate as a side effect;
  // safe to call on every render.
  if (workspace) {
    setCurrentWorkspace(workspaceSlug, workspace.id);
  }

  // Cookie write (last_workspace_slug) — proxy reads it on next page load.
  // ALSO write legacy localStorage["multica_workspace_id"] for forward/back
  // compatibility: if this version ever gets reverted to the pre-refactor
  // build, the legacy code reads that localStorage key to know which
  // workspace to attach to API requests. Without double-writing, a rollback
  // would leave returning users with empty data (API calls would have no
  // X-Workspace-ID header). Forward compatible — new code ignores this key.
  useEffect(() => {
    if (!workspace || typeof document === "undefined") return;
    const oneYear = 60 * 60 * 24 * 365;
    const secure = location.protocol === "https:" ? "; Secure" : "";
    document.cookie = `last_workspace_slug=${encodeURIComponent(workspaceSlug)}; path=/; max-age=${oneYear}; SameSite=Lax${secure}`;
    try {
      localStorage.setItem("multica_workspace_id", workspace.id);
    } catch {
      // localStorage may be unavailable in restricted contexts; non-critical.
    }
  }, [workspace, workspaceSlug]);

  // Remember whether this slug has resolved before. Used below to avoid
  // flashing NoAccessPage during active workspace removal (delete, leave,
  // or realtime eviction) — in those cases the caller is navigating away
  // and we just need to hold null briefly.
  const hasBeenSeen = useWorkspaceSeen(workspaceSlug, !!workspace);

  if (isAuthLoading) return null;
  // Don't render children until workspace is resolved. useWorkspaceId()
  // throws when the list hasn't populated or the slug is unknown — gating
  // here makes that invariant hold for every descendant.
  if (!listFetched) return null;
  if (!workspace) {
    // If we've resolved this slug before in this session, it was just
    // removed from our list (deleted/left/evicted). A navigate is almost
    // certainly in flight — render null to avoid a NoAccessPage flash.
    if (hasBeenSeen) return null;
    // Otherwise: the URL points at a workspace the user never had access
    // to. Show explicit feedback instead of silently redirecting. Doesn't
    // distinguish 404 vs 403 to avoid letting attackers enumerate slugs.
    return <NoAccessPage />;
  }

  return (
    <WorkspaceSlugProvider slug={workspaceSlug}>
      {children}
    </WorkspaceSlugProvider>
  );
}
