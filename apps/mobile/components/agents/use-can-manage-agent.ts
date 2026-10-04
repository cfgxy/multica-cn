/**
 * canManageAgent for the agent sub-screens (RUYI-346) — same rule as the
 * detail screen and the server's canManageAgent: agent owner OR workspace
 * owner/admin. Duplicated from `[id].tsx` as a hook so env/webhooks/skills
 * sheets compute it identically instead of re-deriving member lookups.
 */
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import { memberListOptions } from "@/data/queries/members";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";

export function useCanManageAgent(agent: Agent | undefined): boolean {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const me = useAuthStore((s) => s.user);
  const { data: members } = useQuery(memberListOptions(wsId));

  return useMemo(() => {
    if (!agent) return false;
    const mine = me ? members?.find((m) => m.user_id === me.id) : undefined;
    const isWorkspaceAdmin = mine?.role === "owner" || mine?.role === "admin";
    return isWorkspaceAdmin || (!!me && agent.owner_id === me.id);
  }, [agent, members, me]);
}
