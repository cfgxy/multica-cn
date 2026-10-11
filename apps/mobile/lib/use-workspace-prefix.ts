/**
 * Reactive half of the workspace-prefix resolution (RUYI-705 rework):
 * subscribes the store mirrors and the app-wide workspace list cache, then
 * applies the slug-first/id-second discipline from lib/workspace-prefix.ts.
 */
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { workspaceListOptions } from "@/data/queries/workspaces";
import { useWorkspaceStore } from "@/data/workspace-store";
import { resolveWorkspacePrefix } from "./workspace-prefix";

/** Subscribes the store mirrors + the app-wide workspace list cache. */
export function useWorkspaceIssuePrefix(): string | null {
  const slug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const id = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: workspaces } = useQuery(workspaceListOptions());
  return useMemo(
    () => resolveWorkspacePrefix(workspaces, slug, id),
    [workspaces, slug, id],
  );
}
