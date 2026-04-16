"use client";

import { use, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { WorkspaceSlugProvider, paths } from "@multica/core/paths";
import { workspaceBySlugOptions } from "@multica/core/workspace";
import { setCurrentWorkspace } from "@multica/core/platform";
import { useAuthStore } from "@multica/core/auth";

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
  const router = useRouter();

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

  // Slug doesn't match any workspace the user has access to → bounce to `/`
  // and let the root IndexRedirect pick the first valid workspace (falls to
  // onboarding only when the list is truly empty).
  useEffect(() => {
    if (!user) return;
    if (listFetched && !workspace) router.replace(paths.root());
  }, [user, listFetched, workspace, router]);

  if (isAuthLoading) return null;
  // Don't render children until workspace is resolved. useWorkspaceId()
  // throws when the list hasn't populated or the slug is unknown — gating
  // here makes that invariant hold for every descendant.
  if (!listFetched) return null;
  if (!workspace) return null;

  return (
    <WorkspaceSlugProvider slug={workspaceSlug}>
      {children}
    </WorkspaceSlugProvider>
  );
}
