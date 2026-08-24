import { useQuery } from "@tanstack/react-query";
import { useWorkspaceStore } from "@/data/workspace-store";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import { squadListOptions } from "@/data/queries/squads";
import { useT } from "@/lib/use-t";

/**
 * Resolve actor (member / agent / squad) name + avatar URL from the
 * workspace lists. Mirrors packages/core/workspace/hooks.ts useActorName.
 *
 * Returns synchronous lookup helpers — they read whatever is in the TQ
 * cache. If the lists haven't loaded yet, lookups return null/initials
 * fallback; the row will re-render once data arrives.
 */
export function useActorLookup() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const { t } = useT("common");

  const getName = (
    type: "member" | "agent" | "squad" | null | undefined,
    id: string | null | undefined,
  ): string => {
    if (!type || !id) return t("mobile.actor.system", "System");
    // `.trim() ||` 而非 `??`，也不是裸 `||`：
    //   - `??` 只对 null/undefined 触发。服务端可能返回 name: ""，空串不是
    //     nullish，兜底不生效，空串被原样透传——评论区作者名变空白，拼进
    //     「{{count}} 条消息，来自 {{authors}}」还会产出破碎语句。
    //   - 裸 `||` 修掉了空串，但纯空白（"   "、全角空格、换行）是 truthy，
    //     同样绕过兜底，渲染出的仍是一片空白。视觉结果与空串完全一致。
    // 先 trim 再判真值，三种形态一并收口；根治点在此处而非各个渲染点。
    if (type === "member") {
      const m = members.find((m) => m.user_id === id);
      return m?.name?.trim() || t("mobile.actor.unknown_member", "Unknown");
    }
    if (type === "agent") {
      const a = agents.find((a) => a.id === id);
      return a?.name?.trim() || t("mobile.actor.unknown_agent", "Unknown Agent");
    }
    return (
      squads.find((s) => s.id === id)?.name?.trim() ||
      t("mobile.actor.unknown_squad", "Squad")
    );
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
    return squads.find((s) => s.id === id)?.avatar_url ?? null;
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
