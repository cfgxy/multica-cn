/**
 * Issues belonging to a project. Two-bucket FlatList: Open and Done.
 *
 * Status grouping mirrors web's project detail: anything except `done` and
 * `cancelled` is bucketed as Open. `cancelled` is shown in the Done bucket
 * (web does the same — once a project's issue is cancelled it's effectively
 * out of the active work pile).
 *
 * Behavioral parity: row content uses the same priority icon + identifier
 * + title + status icon layout as my-issues IssueRow so the visual identity
 * is consistent across surfaces.
 */
import { useMemo, useState } from "react";
import { ActivityIndicator, LayoutAnimation, Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import Svg, { Path } from "react-native-svg";
import type { Issue } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { StatusIcon } from "@/components/ui/status-icon";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { projectIssuesOptions } from "@/data/queries/projects";
import { useWorkspaceStore } from "@/data/workspace-store";

interface Props {
  projectId: string;
}

const DONE_STATUSES = new Set(["done", "cancelled"]);

export function ProjectRelatedIssues({ projectId }: Props) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { data, isLoading, error } = useQuery(
    projectIssuesOptions(wsId, projectId),
  );

  // Open expanded by default (active work); Done collapsed (housekeeping).
  // Matches iOS Notes / Reminders pattern of "show what needs attention,
  // hide what's done unless asked".
  const [openExpanded, setOpenExpanded] = useState(true);
  const [doneExpanded, setDoneExpanded] = useState(false);

  const toggleOpen = () => {
    // Native one-shot LayoutAnimation gives a smooth iOS-feeling
    // expand/collapse without pulling in reanimated.
    LayoutAnimation.configureNext(LayoutAnimation.Presets.easeInEaseOut);
    setOpenExpanded((v) => !v);
  };
  const toggleDone = () => {
    LayoutAnimation.configureNext(LayoutAnimation.Presets.easeInEaseOut);
    setDoneExpanded((v) => !v);
  };

  const { open, done } = useMemo(() => {
    const open: Issue[] = [];
    const done: Issue[] = [];
    for (const issue of data ?? []) {
      if (DONE_STATUSES.has(issue.status)) {
        done.push(issue);
      } else {
        open.push(issue);
      }
    }
    return { open, done };
  }, [data]);

  const navigateToIssue = (id: string) => {
    if (wsSlug) router.push(`/${wsSlug}/issue/${id}`);
  };

  if (isLoading) {
    return (
      <View className="px-4 py-6 items-center">
        <ActivityIndicator />
      </View>
    );
  }

  if (error) {
    return (
      <View className="px-4 py-6">
        <Text className="text-sm text-destructive">
          Failed to load issues:{" "}
          {error instanceof Error ? error.message : "unknown error"}
        </Text>
      </View>
    );
  }

  const total = open.length + done.length;
  if (total === 0) {
    return (
      <View className="px-4 py-6">
        <Text className="text-sm text-muted-foreground">
          No issues yet.
        </Text>
      </View>
    );
  }

  return (
    <View>
      <SectionHeader
        title="Open"
        count={open.length}
        expanded={openExpanded}
        onToggle={toggleOpen}
      />
      {openExpanded
        ? open.map((issue) => (
            <IssueRow
              key={issue.id}
              issue={issue}
              onPress={() => navigateToIssue(issue.id)}
            />
          ))
        : null}
      {done.length > 0 ? (
        <>
          <SectionHeader
            title="Done"
            count={done.length}
            expanded={doneExpanded}
            onToggle={toggleDone}
          />
          {doneExpanded
            ? done.map((issue) => (
                <IssueRow
                  key={issue.id}
                  issue={issue}
                  onPress={() => navigateToIssue(issue.id)}
                />
              ))
            : null}
        </>
      ) : null}
    </View>
  );
}

function SectionHeader({
  title,
  count,
  expanded,
  onToggle,
}: {
  title: string;
  count: number;
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <Pressable
      onPress={onToggle}
      className="flex-row items-center gap-2 px-4 py-2 bg-background active:bg-secondary"
    >
      <Chevron expanded={expanded} />
      <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
        {title}
      </Text>
      <Text className="text-xs text-muted-foreground/60">{count}</Text>
    </Pressable>
  );
}

function Chevron({ expanded }: { expanded: boolean }) {
  // ▶ at rest, rotates to ▼ when expanded. Drawn as right-pointing in the
  // SVG so the rotation transform reads as "open" without flipping
  // orientation per state.
  return (
    <View
      style={{
        width: 12,
        height: 12,
        transform: [{ rotate: expanded ? "90deg" : "0deg" }],
      }}
    >
      <Svg width={12} height={12} viewBox="0 0 16 16">
        <Path
          d="M6 4 L10 8 L6 12"
          fill="none"
          stroke="#71717a"
          strokeWidth={1.5}
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </Svg>
    </View>
  );
}

function IssueRow({
  issue,
  onPress,
}: {
  issue: Issue;
  onPress: () => void;
}) {
  return (
    <Pressable onPress={onPress} className="active:bg-secondary px-4 py-3">
      <View className="flex-row items-center gap-3">
        <StatusIcon status={issue.status} size={14} />
        <PriorityIcon priority={issue.priority} size={14} />
        <Text className="text-xs text-muted-foreground shrink-0 w-16">
          {issue.identifier}
        </Text>
        <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
          {issue.title}
        </Text>
        {issue.assignee_id &&
        (issue.assignee_type === "member" || issue.assignee_type === "agent") ? (
          <ActorAvatar
            type={issue.assignee_type}
            id={issue.assignee_id}
            size={20}
          />
        ) : null}
      </View>
    </Pressable>
  );
}
