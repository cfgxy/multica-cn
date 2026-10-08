/**
 * Ambient status badge for the issue detail Stack header (right side).
 * Double state (RUYI-417) — the badge used to render only while a task was
 * active, which erased the runs page's only always-visible entry the moment
 * a run finished:
 *
 *   ≥1 active task        → breathing avatar stack         (live badge;
 *                           RUYI-554: running agents breathe, queued-only
 *                           stacks gray — no dot beside the stack)
 *   0 active, ≥1 past run → clock + "Runs · N"             (history entry)
 *   never run             → null (no chrome, no placeholder)
 *
 * Why this exists: the in-card `<AgentActivityRow>` is the first-time-
 * discovery surface (full "Working" text + larger avatars), but it scrolls
 * away with the timeline. Agent tasks run for minutes to tens of minutes;
 * users actively scroll during that window to read past comments. The
 * "is anything still working / has anything run" signal needs a consistent
 * location — see Apple HIG "Progress Indicators" + the agent-UX "ambient
 * status badge" pattern
 * (https://www.aiuxdesign.guide/patterns/agent-status-monitoring).
 *
 * Tap pushes the `issue/[id]/runs` formSheet route in both states — the
 * in-card AgentActivityRow does the same. One route, two entry points, no
 * duplicate sheet state. The idle copy reuses the row's
 * `mobile.agent_activity.runs_count` key so both surfaces read alike.
 */
import { useMemo } from "react";
import { Pressable } from "react-native";
import { router } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { Ionicons } from "@expo/vector-icons";
import { AvatarStack, type StackActor } from "@/components/ui/avatar-stack";
import { Text } from "@/components/ui/text";
import {
  issueActiveTasksOptions,
  issueTasksOptions,
} from "@/data/queries/issues";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

interface Props {
  issueId: string;
}

export function AgentHeaderBadge({ issueId }: Props) {
  const { t } = useT("issues");
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const mutedFg = THEME[colorScheme].mutedForeground;
  const { data: active = [] } = useQuery(
    issueActiveTasksOptions(wsId, issueId),
  );
  const { data: allTasks = [] } = useQuery(issueTasksOptions(wsId, issueId));

  // "Past" = terminal runs on record. The tasks endpoint returns every
  // status, so the count derives from the list the Runs sheet already holds.
  // Inline filter, same as runs.tsx and agent-activity-row.tsx.
  const pastCount = useMemo(
    () =>
      allTasks.filter(
        (t) =>
          t.status === "completed" ||
          t.status === "failed" ||
          t.status === "cancelled",
      ).length,
    [allTasks],
  );

  const openRuns = () => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/issue/[id]/runs",
      params: { workspace: wsSlug, id: issueId },
    });
  };

  if (active.length === 0) {
    // Nothing live, but history exists → calm clock + count entry. Whole-
    // string interpolation: CJK quantifier and separator placement differ
    // from English, so no "Runs · " + count concatenation (same as the row).
    if (pastCount === 0) return null;
    return (
      <Pressable
        onPress={openRuns}
        hitSlop={8}
        accessibilityLabel={t(
          "mobile.agent_activity.badge_idle_a11y",
          "View past runs",
        )}
        className="flex-row items-center gap-1 px-2 py-1 active:opacity-60"
      >
        <Ionicons name="time-outline" size={14} color={mutedFg} />
        <Text className="text-xs text-muted-foreground">
          {t("mobile.agent_activity.runs_count", "Runs · {{count}}", {
            count: pastCount,
          })}
        </Text>
      </Pressable>
    );
  }

  const actors = active.map<StackActor>((t) => ({
    type: "agent",
    id: t.agent_id,
  }));
  // RUYI-554: the stack carries the state itself — running agents breathe,
  // a queued-only stack sits gray. Same bucket rule as the row badge.
  const hasRunning = active.some((t) => t.status === "running");

  return (
    <Pressable
      onPress={openRuns}
      hitSlop={8}
      accessibilityLabel={t(
        "mobile.agent_activity.badge_a11y",
        "Agent working — open runs",
      )}
      className="flex-row items-center gap-1.5 px-2 py-1 active:opacity-60"
    >
      <AvatarStack
        actors={actors}
        max={2}
        size={20}
        activity={hasRunning ? "running" : "queued"}
      />
    </Pressable>
  );
}
