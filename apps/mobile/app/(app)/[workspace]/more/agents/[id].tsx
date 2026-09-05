/**
 * Per-agent active-run list (`more/agents/[id]`, RUYI-76 ②) — the answer to
 * "what is THIS agent running right now". Header carries the agent identity
 * + the same presence line as the list screen; below it, the agent's active
 * tasks (running first, then queued — `selectAgentActiveTasks`) sliced from
 * the shared workspace snapshot.
 *
 * Data sources (all cache-shared, no new endpoints):
 *   - agents list            → agent identity (name/avatar) + 404 state
 *   - workspace presence map → availability/workload detail
 *   - agent-task-snapshot    → the active runs
 *   - workspace issue list   → issue titles for linked runs. One workspace
 *     list fetch shared with `more/issues`; when it hasn't landed (or an
 *     issue became invisible) the row falls back to a static label instead
 *     of blocking the run list.
 *
 * WS freshness comes from `use-presence-realtime` (snapshot + agents
 * invalidation on lifecycle events); pull-to-refresh covers the rest.
 */
import { useMemo } from "react";
import { FlatList, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { useLocalSearchParams } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { AgentPresenceLine } from "@/components/agents/agent-presence-line";
import { AgentTaskRow } from "@/components/agents/agent-task-row";
import { agentListOptions } from "@/data/queries/agents";
import { issueListOptions } from "@/data/queries/issues";
import { agentTaskSnapshotOptions } from "@/data/queries/agent-task-snapshot";
import { selectAgentActiveTasks } from "@/lib/issue-agent-activity";
import { useWorkspacePresenceMap } from "@/lib/use-agent-presence";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function AgentDetailPage() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const agentId = typeof id === "string" ? id : null;
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("agents");

  const { data: agents, isLoading: agentsLoading, refetch: refetchAgents } =
    useQuery(agentListOptions(wsId));
  const agent = useMemo(
    () => agents?.find((a) => a.id === agentId) ?? null,
    [agents, agentId],
  );

  const { byAgent } = useWorkspacePresenceMap(wsId);
  const detail = agentId ? byAgent.get(agentId) : undefined;

  const { data: snapshot = [], refetch: refetchSnapshot } = useQuery(
    agentTaskSnapshotOptions(wsId),
  );
  const activeTasks = useMemo(
    () => (agentId ? selectAgentActiveTasks(snapshot, agentId) : []),
    [snapshot, agentId],
  );

  // Title lookup only — an error here must NOT blank the run list; rows
  // render their fallback label instead.
  const { data: issues, refetch: refetchIssues } = useQuery(issueListOptions(wsId));
  const titleById = useMemo(() => {
    const map = new Map<string, string>();
    for (const issue of issues ?? []) map.set(issue.id, issue.title);
    return map;
  }, [issues]);

  const onRefresh = () => {
    refetchAgents();
    refetchSnapshot();
    refetchIssues();
  };

  if (agentsLoading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Text className="text-sm text-muted-foreground">
          {t("page.list_loading", "Loading agents…")}
        </Text>
      </View>
    );
  }

  if (!agent) {
    return (
      <View className="flex-1 items-center justify-center bg-background px-8 gap-2">
        <Ionicons
          name="help-circle-outline"
          size={42}
          color={THEME[colorScheme].mutedForeground}
        />
        <Text className="text-sm text-muted-foreground text-center">
          {t("mobile.tasks.agent_missing", "Agent not found.")}
        </Text>
      </View>
    );
  }

  return (
    <FlatList
      className="flex-1 bg-background"
      data={activeTasks}
      keyExtractor={(task) => task.id}
      ItemSeparatorComponent={() => <View className="h-px bg-border ml-4" />}
      contentContainerClassName="pb-6"
      refreshing={false}
      onRefresh={onRefresh}
      ListHeaderComponent={
        <View className="flex-row items-center gap-3 px-4 py-4 border-b border-border">
          <ActorAvatar type="agent" id={agent.id} size={44} />
          <View className="flex-1 min-w-0 gap-1">
            <Text
              className="text-base font-semibold text-foreground"
              numberOfLines={1}
            >
              {agent.name}
            </Text>
            {detail ? <AgentPresenceLine detail={detail} /> : null}
          </View>
        </View>
      }
      ListEmptyComponent={
        <View className="flex-1 items-center justify-center px-8 gap-2 pt-24">
          <Ionicons
            name="moon-outline"
            size={42}
            color={THEME[colorScheme].mutedForeground}
          />
          <Text className="text-sm text-muted-foreground text-center">
            {t("mobile.tasks.empty", "No active runs right now.")}
          </Text>
        </View>
      }
      renderItem={({ item }) => (
        <AgentTaskRow
          task={item}
          issueTitle={item.issue_id ? (titleById.get(item.issue_id) ?? null) : null}
          wsSlug={wsSlug}
        />
      )}
    />
  );
}
