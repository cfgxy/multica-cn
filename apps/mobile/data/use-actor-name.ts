import { useQuery } from "@tanstack/react-query";
import { useWorkspaceStore } from "@/data/workspace-store";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";

/**
 * Resolve actor (member or agent) name + avatar URL from the workspace
 * member/agent lists. Mirrors packages/core/workspace/hooks.ts useActorName.
 *
 * Returns synchronous lookup helpers — they read whatever is in the TQ
 * cache. If the lists haven't loaded yet, lookups return null/initials
 * fallback; the row will re-render once data arrives.
 */
export function useActorLookup() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  const getName = (
    type: "member" | "agent" | "squad" | null | undefined,
    id: string | null | undefined,
  ): string => {
    if (!type || !id) return "System";
    if (type === "member") {
      const m = members.find((m) => m.user_id === id);
      return m?.name ?? "Unknown";
    }
    if (type === "agent") {
      const a = agents.find((a) => a.id === id);
      return a?.name ?? "Unknown Agent";
    }
    // Mobile has no squad list query yet — render a generic label so squad
    // assignees coming from web/desktop are visible (and clearable) here.
    return "Squad";
  };

  const getAvatarUrl = (
    type: "member" | "agent" | "squad" | null | undefined,
    id: string | null | undefined,
  ): string | null => {
    if (!type || !id) return null;
    if (type === "member") {
      return members.find((m) => m.user_id === id)?.avatar_url ?? null;
    }
    if (type === "agent") {
      return agents.find((a) => a.id === id)?.avatar_url ?? null;
    }
    // No squad cache — ActorAvatar falls back to the group glyph.
    return null;
  };

  return { getName, getAvatarUrl };
}

export function getInitials(name: string): string {
  return name
    .split(" ")
    .map((w) => w[0])
    .filter(Boolean)
    .join("")
    .toUpperCase()
    .slice(0, 2);
}
