/**
 * "My Issues" tab. Three scopes — assigned / created / agents — mirroring
 * web's `packages/views/my-issues/components/my-issues-page.tsx:48-65`. The
 * `agents` scope label is "Agents and Squads" because the backend predicate
 * (`involves_user_id`, MUL-2397) surfaces both the user's owned agents and
 * squads they're involved in (member / leader / has an owned agent inside).
 *
 * Issues are grouped by status using SectionList in `BOARD_STATUSES` order;
 * empty status sections are filtered out so the screen doesn't fill with
 * "(0)" headers. Section grouping uses `BOARD_STATUSES` (cancelled excluded)
 * to match web — same source `packages/views/my-issues/components/my-issues-page.tsx:117-125`.
 *
 * Status + Priority filters mirror web's MyIssuesHeader filter sub-menus.
 * Filter state lives in `useMyIssuesViewStore` and is cleared on workspace
 * change via the shared `useClearFiltersOnWorkspaceChange` hook.
 */
import { useMemo } from "react";
import { Pressable, SectionList, View } from "react-native";
import SegmentedControl from "@react-native-segmented-control/segmented-control";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type { Issue, IssuePriority, IssueStatus } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Header } from "@/components/ui/header";
import { IconButton } from "@/components/ui/icon-button";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { StatusIcon } from "@/components/ui/status-icon";
import { IssueRow } from "@/components/issue/issue-row";
import { IssuesLoading } from "@/components/issue/issues-loading";
import {
  buildMyIssuesFilter,
  myIssueListOptions,
} from "@/data/queries/my-issues";
import type { MyIssuesScope } from "@/data/queries/issue-keys";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useMyIssuesViewStore } from "@/data/stores/my-issues-view-store";
import { useClearFiltersOnWorkspaceChange } from "@/lib/use-clear-filters-on-workspace-change";
import {
  BOARD_STATUSES,
  PRIORITY_LABEL,
  STATUS_LABEL,
} from "@/lib/issue-status";
import { filterIssues } from "@/lib/filter-issues";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";

// Label "Agents and Squads" mirrors web `packages/views/locales/en/my-issues.json:14`
// ("My Agents and Squads"). The scope value stays "agents" — that's the
// cache key + view-store discriminator; user-visible string is what changed.
const SCOPES: { value: MyIssuesScope; label: string }[] = [
  { value: "assigned", label: "Assigned" },
  { value: "created", label: "Created" },
  { value: "agents", label: "Agents and Squads" },
];

type IssueSection = { status: IssueStatus; data: Issue[] };

export default function MyIssues() {
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);

  const scope = useMyIssuesViewStore((s) => s.scope);
  const setScope = useMyIssuesViewStore((s) => s.setScope);
  const statusFilters = useMyIssuesViewStore((s) => s.statusFilters);
  const priorityFilters = useMyIssuesViewStore((s) => s.priorityFilters);

  const openFilter = () => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/issues-filter",
      params: { workspace: wsSlug, scope: "my" },
    });
  };

  useClearFiltersOnWorkspaceChange(
    useMyIssuesViewStore.getState().clearFilters,
    wsId,
  );

  const filter = useMemo(
    () => (userId ? buildMyIssuesFilter(scope, userId) : { assignee_id: "" }),
    [scope, userId],
  );

  const { data, isLoading, error, refetch, isRefetching } = useQuery({
    ...myIssueListOptions(wsId, scope, filter),
    enabled: !!wsId && !!userId,
  });

  // Apply client-side status + priority filter. Mirrors the predicate at
  // packages/views/issues/utils/filter.ts:30-34 via filterIssues().
  const filtered = useMemo(
    () => filterIssues(data ?? [], statusFilters, priorityFilters),
    [data, statusFilters, priorityFilters],
  );

  // When statusFilters is non-empty, intersect visible status order with it
  // so hidden statuses don't render an empty section header. Uses
  // BOARD_STATUSES (cancelled excluded) to match web.
  const sections = useMemo<IssueSection[]>(() => {
    if (filtered.length === 0) return [];
    const byStatus = new Map<IssueStatus, Issue[]>();
    for (const issue of filtered) {
      const list = byStatus.get(issue.status);
      if (list) list.push(issue);
      else byStatus.set(issue.status, [issue]);
    }
    const visibleStatuses = statusFilters.length > 0
      ? BOARD_STATUSES.filter((s) => statusFilters.includes(s))
      : BOARD_STATUSES;
    return visibleStatuses
      .map((status) => ({ status, data: byStatus.get(status) ?? [] }))
      .filter((s) => s.data.length > 0);
  }, [filtered, statusFilters]);

  const hasActiveFilters =
    statusFilters.length > 0 || priorityFilters.length > 0;

  const showEmptyState =
    !isLoading && !error && filtered.length === 0;

  const selectedIndex = SCOPES.findIndex((s) => s.value === scope);

  return (
    <View className="flex-1 bg-background">
      <Header
        title="My Issues"
        right={
          <>
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
            <HeaderActions />
          </>
        }
      />
      <View className="px-4 pt-2 pb-2">
        <SegmentedControl
          values={SCOPES.map((s) => s.label)}
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
            useMyIssuesViewStore.getState().toggleStatusFilter(s)
          }
          onClearPriority={(p) =>
            useMyIssuesViewStore.getState().togglePriorityFilter(p)
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
            <SectionHeader
              status={section.status}
              count={section.data.length}
            />
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
        <Chip key={`s-${s}`} label={STATUS_LABEL[s]} onClear={() => onClearStatus(s)} />
      ))}
      {priorityFilters.map((p) => (
        <Chip key={`p-${p}`} label={PRIORITY_LABEL[p]} onClear={() => onClearPriority(p)} />
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

function emptyMessageForScope(scope: MyIssuesScope): string {
  switch (scope) {
    case "assigned":
      return "No issues assigned to you.";
    case "created":
      return "You haven't created any issues.";
    case "agents":
      return "No issues assigned to your agents or squads yet.";
  }
}

