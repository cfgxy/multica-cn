/**
 * Workspace agents list — mobile's agent perspective (RUYI-76 ②).
 * Replaces the "Agents coming soon" stub: every non-archived agent with its
 * live presence line (availability + workload + counts), tap through to the
 * agent's active runs (`more/agents/[id]`).
 *
 * Data: agents / runtimes / agent-task-snapshot via
 * `useWorkspacePresenceMap` — the same three queries the workspace prefetch
 * warms on entry and `use-presence-realtime` keeps fresh, so this screen
 * normally paints from warm caches.
 *
 * Parity notes (apps/mobile/CLAUDE.md):
 * - Presence semantics come from the SHARED pure derivation
 *   (`buildPresenceMap` in @multica/core/agents) — same enums and colours
 *   as web's Agents page; labels reuse the shared agents locale namespace.
 * - Documented divergence vs web's agents page: no scope tabs / sort menus /
 *   sparkline columns — mobile answers the single monitoring question
 *   "who is busy, on what", so rows sort working → queued → idle (name
 *   asc within each band). Archived agents are hidden, matching web's
 *   default non-archived scope.
 */
import { useMemo } from "react";
import { FlatList, Pressable, View } from "react-native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import type { Workload } from "@multica/core/agents";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { AgentPresenceLine } from "@/components/agents/agent-presence-line";
import { agentListOptions } from "@/data/queries/agents";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useWorkspacePresenceMap } from "@/lib/use-agent-presence";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

const WORKLOAD_RANK: Record<Workload, number> = {
  working: 0,
  queued: 1,
  idle: 2,
};

export default function AgentsPage() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");
  const { data: agents, isLoading: agentsLoading } = useQuery(
    agentListOptions(wsId),
  );
  const { byAgent, loading: presenceLoading } = useWorkspacePresenceMap(wsId);

  const visible = useMemo(() => {
    // Working first, then queued, then idle — the monitoring order the
    // screen exists for. Name asc inside each band so the order is stable
    // across presence refreshes. Archived agents are hidden (web's default
    // non-archived scope).
    return (agents ?? [])
      .filter((agent) => !agent.archived_at)
      .sort((a, b) => {
        const rankA = WORKLOAD_RANK[byAgent.get(a.id)?.workload ?? "idle"];
        const rankB = WORKLOAD_RANK[byAgent.get(b.id)?.workload ?? "idle"];
        if (rankA !== rankB) return rankA - rankB;
        return a.name.localeCompare(b.name);
      });
  }, [agents, byAgent]);
  const loading = (agentsLoading || presenceLoading) && visible.length === 0;

  if (loading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Text className="text-sm text-muted-foreground">
          {t("page.list_loading", "Loading agents…")}
        </Text>
      </View>
    );
  }

  return (
    <FlatList
      className="flex-1 bg-background"
      data={visible}
      keyExtractor={(agent) => agent.id}
      ItemSeparatorComponent={() => <View className="h-px bg-border ml-16" />}
      contentContainerClassName="pb-6"
      ListEmptyComponent={
        <View className="flex-1 items-center justify-center px-8 gap-2 pt-24">
          <Ionicons
            name="people-outline"
            size={42}
            color={THEME[colorScheme].mutedForeground}
          />
          <Text className="text-base font-medium text-foreground text-center">
            {t("empty.title", "No agents yet")}
          </Text>
          <Text className="text-sm text-muted-foreground text-center">
            {t(
              "empty.description",
              "Create an agent and assign it issues, like any teammate. Local agents run on your machine; cloud agents run on Multica's runtime.",
            )}
          </Text>
        </View>
      }
      renderItem={({ item }) => {
        const detail = byAgent.get(item.id);
        if (!detail) return null;
        return (
          <Pressable
            onPress={() => {
              if (!wsSlug) return;
              router.push({
                pathname: "/[workspace]/more/agents/[id]",
                params: { workspace: wsSlug, id: item.id },
              });
            }}
            className="flex-row items-center gap-3 bg-background active:bg-secondary px-4 py-3"
            accessibilityLabel={item.name}
          >
            <ActorAvatar type="agent" id={item.id} size={40} />
            <View className="flex-1 min-w-0 gap-1">
              <Text
                className="text-sm font-medium text-foreground"
                numberOfLines={1}
              >
                {item.name}
              </Text>
              <AgentPresenceLine detail={detail} />
            </View>
            <Ionicons
              name="chevron-forward"
              size={16}
              color={THEME[colorScheme].mutedForeground}
            />
          </Pressable>
        );
      }}
    />
  );
}
