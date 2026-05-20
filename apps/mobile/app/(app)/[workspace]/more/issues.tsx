/**
 * Workspace-wide Issues page. Mirrors web `packages/views/issues/components/
 * issues-page.tsx:32-94`: fetch every issue in the workspace, expose
 * `all / members / agents` scope tabs, group by status, allow status +
 * priority filtering.
 *
 * Scope is a **client-side** filter on `assignee_type` — matches web
 * `issues-page.tsx:90-94`. This keeps `issueListOptions(wsId)` workspace-
 * scoped (no scope param on the wire), so `issueKeys.list(wsId)` and
 * `useIssuesRealtime` need no changes.
 *
 * Differences vs My Issues (`(tabs)/my-issues.tsx`):
 *   - Workspace-wide list (all issues), not user-scoped.
 *   - Three scopes are `all / members / agents` (assignee_type pre-filter),
 *     not `assigned / created / agents` (per-user predicates).
 *   - Independent filter store (`useIssuesViewStore`) so workspace-level
 *     filters don't bleed into the per-user view.
 *
 * Filters beyond status/priority (assignee / project / label / creator)
 * are deferred — power-user features with non-trivial picker cost; ship
 * after the parity-critical scope tabs land.
 */
import { useLayoutEffect, useMemo } from "react";
import { Pressable, SectionList, View } from "react-native";
import SegmentedControl from "@react-native-segmented-control/segmented-control";
import { useQuery } from "@tanstack/react-query";
import { router, useNavigation } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type { Issue, IssuePriority, IssueStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { IconButton } from "@/components/ui/icon-button";
import { StatusIcon } from "@/components/ui/status-icon";
import { IssueRow } from "@/components/issue/issue-row";
import { IssuesLoading } from "@/components/issue/issues-loading";
import { issueListOptions } from "@/data/queries/issues";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  useIssuesViewStore,
  type IssuesScope,
} from "@/data/stores/issues-view-store";
import { useClearFiltersOnWorkspaceChange } from "@/lib/use-clear-filters-on-workspace-change";
import {
  BOARD_STATUSES,
  PRIORITY_LABEL,
  STATUS_LABEL,
} from "@/lib/issue-status";
import { filterIssues } from "@/lib/filter-issues";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";

type IssueSection = { status: IssueStatus; data: Issue[] };

// Scope tab definitions. Same value set as web `issuesScopeStore`. Labels
// stay English (mobile is not i18n'd in v1); counts get appended at render
// time from `scopeCounts` so users see "(N)" per tab matching web's
// `useIssueCounts` (`issues-header.tsx:114-153`).
const SCOPES: { value: IssuesScope; label: string }[] = [
  { value: "all", label: "All" },
  { value: "members", label: "Members" },
  { value: "agents", label: "Agents" },
];

export default function IssuesPage() {
  const navigation = useNavigation();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);

  const scope = useIssuesViewStore((s) => s.scope);
  const setScope = useIssuesViewStore((s) => s.setScope);
  const statusFilters = useIssuesViewStore((s) => s.statusFilters);
  const priorityFilters = useIssuesViewStore((s) => s.priorityFilters);

  const openFilter = () => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/issues-filter",
      params: { workspace: wsSlug, scope: "all" },
    });
  };

  useClearFiltersOnWorkspaceChange(
    useIssuesViewStore.getState().clearFilters,
    wsId,
  );

  const { data, isLoading, error, refetch, isRefetching } = useQuery(
    issueListOptions(wsId),
  );

  const allIssues = data ?? [];

  // Counts per scope — derived once from the raw list, used to label the
  // SegmentedControl. `agents` includes both agent + squad assignees to
  // match web `issues-page.tsx:93`.
  const scopeCounts = useMemo(() => {
    let members = 0;
    let agents = 0;
    for (const issue of allIssues) {
      if (issue.assignee_type === "member") members++;
      else if (
        issue.assignee_type === "agent" ||
        issue.assignee_type === "squad"
      ) {
        agents++;
      }
    }
    return { all: allIssues.length, members, agents };
  }, [allIssues]);

  // Scope pre-filter — mirrors web `issues-page.tsx:90-94`. Applied before
  // status/priority filtering so chip filters operate on the visible slice.
  const scopedIssues = useMemo(() => {
    if (scope === "members") {
      return allIssues.filter((i) => i.assignee_type === "member");
    }
    if (scope === "agents") {
      return allIssues.filter(
        (i) => i.assignee_type === "agent" || i.assignee_type === "squad",
      );
    }
    return allIssues;
  }, [allIssues, scope]);

  const filtered = useMemo(
    () => filterIssues(scopedIssues, statusFilters, priorityFilters),
    [scopedIssues, statusFilters, priorityFilters],
  );

  // Section grouping uses BOARD_STATUSES (cancelled excluded) — matches web
  // `issues-page.tsx:117-125`.
  const sections = useMemo<IssueSection[]>(() => {
    if (filtered.length === 0) return [];
    const byStatus = new Map<IssueStatus, Issue[]>();
    for (const issue of filtered) {
      const list = byStatus.get(issue.status);
      if (list) list.push(issue);
      else byStatus.set(issue.status, [issue]);
    }
    const visibleStatuses =
      statusFilters.length > 0
        ? BOARD_STATUSES.filter((s) => statusFilters.includes(s))
        : BOARD_STATUSES;
    return visibleStatuses
      .map((status) => ({ status, data: byStatus.get(status) ?? [] }))
      .filter((s) => s.data.length > 0);
  }, [filtered, statusFilters]);

  const hasActiveFilters =
    statusFilters.length > 0 || priorityFilters.length > 0;

  const showEmptyState = !isLoading && !error && filtered.length === 0;

  const selectedIndex = SCOPES.findIndex((s) => s.value === scope);

  // Native Stack header owns the chrome — we feed it the filter IconButton
  // (with an absolute red dot when filters are active) via headerRight.
  useLayoutEffect(() => {
    navigation.setOptions({
      headerRight: () => (
        <View style={{ position: "relative" }}>
          <IconButton
            name="options-outline"
            onPress={openFilter}
            accessibilityLabel="Filter"
          />
          {hasActiveFilters ? (
            <View
              pointerEvents="none"
              className="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-brand"
            />
          ) : null}
        </View>
      ),
    });
  });

  return (
    <View className="flex-1 bg-background">
      <View className="px-4 pt-2 pb-2">
        <SegmentedControl
          values={SCOPES.map(
            (s) => `${s.label} (${scopeCounts[s.value]})`,
          )}
          selectedIndex={selectedIndex === -1 ? 0 : selectedIndex}
          onChange={(e) =>
            setScope(SCOPES[e.nativeEvent.selectedSegmentIndex].value)
          }
        />
      </View>
      {hasActiveFilters ? (
        <ActiveFilterChips
          statusFilters={statusFilters}
          priorityFilters={priorityFilters}
          onClearStatus={(s) =>
            useIssuesViewStore.getState().toggleStatusFilter(s)
          }
          onClearPriority={(p) =>
            useIssuesViewStore.getState().togglePriorityFilter(p)
          }
        />
      ) : null}
      {isLoading ? (
        <IssuesLoading />
      ) : error ? (
        <View className="px-4 gap-3 pt-4">
          <Text className="text-sm text-destructive">
            Failed to load issues:{" "}
            {error instanceof Error ? error.message : "unknown error"}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            <Text>Retry</Text>
          </Button>
        </View>
      ) : showEmptyState ? (
        <EmptyState
          message={
            hasActiveFilters
              ? "No issues match the current filters."
              : emptyMessageForScope(scope)
          }
        />
      ) : (
        <SectionList
          sections={sections}
          keyExtractor={(item) => item.id}
          stickySectionHeadersEnabled={false}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-4" />
          )}
          renderSectionHeader={({ section }) => (
            <SectionHeader status={section.status} count={section.data.length} />
          )}
          contentContainerClassName="pb-6"
          renderItem={({ item }) => (
            <IssueRow
              issue={item}
              onPress={() => {
                if (wsSlug) router.push(`/${wsSlug}/issue/${item.id}`);
              }}
            />
          )}
          refreshing={isRefetching}
          onRefresh={refetch}
        />
      )}
    </View>
  );
}

function ActiveFilterChips({
  statusFilters,
  priorityFilters,
  onClearStatus,
  onClearPriority,
}: {
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  onClearStatus: (s: IssueStatus) => void;
  onClearPriority: (p: IssuePriority) => void;
}) {
  return (
    <View className="flex-row flex-wrap gap-1.5 px-4 pb-2">
      {statusFilters.map((s) => (
        <Chip
          key={`s-${s}`}
          label={STATUS_LABEL[s]}
          onClear={() => onClearStatus(s)}
        />
      ))}
      {priorityFilters.map((p) => (
        <Chip
          key={`p-${p}`}
          label={PRIORITY_LABEL[p]}
          onClear={() => onClearPriority(p)}
        />
      ))}
    </View>
  );
}

function Chip({ label, onClear }: { label: string; onClear: () => void }) {
  const { colorScheme } = useColorScheme();
  return (
    <Pressable
      onPress={onClear}
      className="flex-row items-center gap-1 pl-2.5 pr-2 py-1 rounded-full border border-border bg-secondary/40 active:bg-secondary"
    >
      <Text className="text-xs text-foreground">{label}</Text>
      <Ionicons
        name="close"
        size={12}
        color={THEME[colorScheme].mutedForeground}
      />
    </Pressable>
  );
}

function SectionHeader({
  status,
  count,
}: {
  status: IssueStatus;
  count: number;
}) {
  return (
    <View className="flex-row items-center gap-2 px-4 py-2 bg-background">
      <StatusIcon status={status} size={14} />
      <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
        {STATUS_LABEL[status]}
      </Text>
      <Text className="text-xs text-muted-foreground/60">{count}</Text>
    </View>
  );
}

function EmptyState({ message }: { message: string }) {
  return (
    <View className="flex-1 items-center justify-center px-6">
      <Text className="text-sm text-muted-foreground text-center">
        {message}
      </Text>
    </View>
  );
}

function emptyMessageForScope(scope: IssuesScope): string {
  switch (scope) {
    case "all":
      return "No issues in this workspace.";
    case "members":
      return "No issues assigned to a member.";
    case "agents":
      return "No issues assigned to agents or squads.";
  }
}
