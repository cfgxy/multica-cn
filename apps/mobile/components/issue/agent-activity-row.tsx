/**
 * Double-state row that lives inside `IssueHeaderCard`. Opens `RunsSheet`
 * on tap.
 *
 *   ≥1 active task        → [agent avatars] (pulse) Working           ›
 *   0 active, ≥1 past     → 🕓 Runs · N                                ›
 *   never run             → null (zero space)
 *
 * Why this lives in IssueHeaderCard and not as a sticky header above the
 * timeline: see /Users/qingnaiyuan/.claude/plans/ok-plan-linked-taco.md
 * for the design rationale. Short version: HIG "indicators near the
 * content they describe" + binary live state doesn't warrant the timeline-
 * restructuring cost of `stickyHeaderIndices`.
 */
import { useEffect, useMemo, useState } from "react";
import { Pressable, View } from "react-native";
import Animated, {
  useAnimatedStyle,
  useSharedValue,
  withRepeat,
  withTiming,
} from "react-native-reanimated";
import { useQuery } from "@tanstack/react-query";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { AvatarStack, type StackActor } from "@/components/ui/avatar-stack";
import {
  issueActiveTasksOptions,
  issueTasksOptions,
} from "@/data/queries/issues";
import { useWorkspaceStore } from "@/data/workspace-store";
import { RunsSheet } from "./runs-sheet";

interface Props {
  issueId: string;
}

export function AgentActivityRow({ issueId }: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const [sheetOpen, setSheetOpen] = useState(false);

  const { data: activeTasks = [] } = useQuery(
    issueActiveTasksOptions(wsId, issueId),
  );
  const { data: allTasks = [] } = useQuery(issueTasksOptions(wsId, issueId));

  const activeCount = activeTasks.length;
  // "Past" = tasks not currently active. The /task-runs endpoint returns the
  // full list, so we filter rather than fetching a separate past-only query.
  // Memo'd so the array reference is stable across renders — RunsSheet's
  // internal useMemo([pastTasks]) only recomputes when the upstream cache
  // actually changes, not on every parent render.
  const pastTasks = useMemo(
    () =>
      allTasks.filter(
        (t) =>
          t.status === "completed" ||
          t.status === "failed" ||
          t.status === "cancelled",
      ),
    [allTasks],
  );
  const pastCount = pastTasks.length;

  if (activeCount === 0 && pastCount === 0) {
    return null;
  }

  const onPress = () => setSheetOpen(true);

  return (
    <>
      <Pressable
        onPress={onPress}
        className="flex-row items-center gap-2 -mx-2 px-2 py-2 rounded-lg active:bg-secondary"
      >
        {activeCount > 0 ? (
          <ActiveContent
            actors={activeTasks.map<StackActor>((t) => ({
              type: "agent",
              id: t.agent_id,
            }))}
          />
        ) : (
          <IdleContent count={pastCount} />
        )}
        <Ionicons name="chevron-forward" size={16} color="#a1a1aa" />
      </Pressable>
      <RunsSheet
        visible={sheetOpen}
        onClose={() => setSheetOpen(false)}
        issueId={issueId}
        activeTasks={activeTasks}
        pastTasks={pastTasks}
      />
    </>
  );
}

function ActiveContent({ actors }: { actors: StackActor[] }) {
  return (
    <View className="flex-1 flex-row items-center gap-2">
      <AvatarStack actors={actors} max={3} size={24} />
      <PulseDot />
      <Text className="text-sm font-medium text-foreground">Working</Text>
    </View>
  );
}

function IdleContent({ count }: { count: number }) {
  return (
    <View className="flex-1 flex-row items-center gap-2">
      <Ionicons name="time-outline" size={16} color="#a1a1aa" />
      <Text className="text-sm text-foreground">Runs · {count}</Text>
    </View>
  );
}

/** Slow green pulse — 2s opacity oscillation on the UI thread (via
 *  Reanimated's `withRepeat`). Same library as comment-card.tsx so no new
 *  animation primitive is introduced. */
function PulseDot() {
  const opacity = useSharedValue(0.3);
  useEffect(() => {
    opacity.value = withRepeat(
      withTiming(1, { duration: 1000 }),
      -1, // infinite
      true, // reverse — yields 0.3 ↔ 1.0 oscillation over 2s
    );
  }, [opacity]);

  const style = useAnimatedStyle(() => ({ opacity: opacity.value }));

  return (
    <Animated.View
      style={[
        { width: 8, height: 8, borderRadius: 4, backgroundColor: "#22c55e" }, // success
        style,
      ]}
    />
  );
}
