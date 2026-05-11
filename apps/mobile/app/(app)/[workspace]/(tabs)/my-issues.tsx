/**
 * "My Issues" tab. Three scopes — assigned / created / agents — mirroring
 * web's `packages/views/my-issues/components/my-issues-page.tsx`.
 *
 * Visual baseline mirrors the inbox tab (apps/mobile/CLAUDE.md "Visual
 * alignment is baseline"): SafeAreaView + ScreenHeader + scroll body.
 * Issues are grouped by status using SectionList in `BOARD_STATUSES` order;
 * empty status sections are filtered out so the screen doesn't fill with
 * "(0)" headers.
 *
 * Status + Priority filters mirror web's MyIssuesHeader filter sub-menus
 * (packages/views/my-issues/components/my-issues-header.tsx). Filter state
 * lives in `useMyIssuesViewStore` and is cleared on workspace change to
 * mirror `useClearFiltersOnWorkspaceChange` in
 * packages/core/issues/stores/view-store.ts:273-284.
 */
import { useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  SectionList,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import Svg, { Line } from "react-native-svg";
import type {
  Agent,
  Issue,
  IssuePriority,
  IssueStatus,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { ScreenHeader } from "@/components/ui/screen-header";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { StatusIcon } from "@/components/ui/status-icon";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { MyIssuesFilterSheet } from "@/components/issue/my-issues-filter-sheet";
import {
  buildMyIssuesFilter,
  myIssueListOptions,
} from "@/data/queries/my-issues";
import { agentListOptions } from "@/data/queries/agents";
import type { MyIssuesScope } from "@/data/queries/issue-keys";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useMyIssuesViewStore } from "@/data/stores/my-issues-view-store";
import {
  BOARD_STATUSES,
  PRIORITY_LABEL,
  STATUS_LABEL,
} from "@/lib/issue-status";
import { filterMyIssues } from "@/lib/filter-issues";
import { cn } from "@/lib/utils";

const SCOPES: { value: MyIssuesScope; label: string }[] = [
  { value: "assigned", label: "Assigned" },
  { value: "created", label: "Created" },
  { value: "agents", label: "Agents" },
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

  const [sheetOpen, setSheetOpen] = useState(false);

  // Mirror useClearFiltersOnWorkspaceChange in
  // packages/core/issues/stores/view-store.ts:273-284 — clear filters on
  // transitions between two defined workspace ids (ref guard skips the
  // first render so we don't wipe initial state on mount).
  const prevWsRef = useRef<string | null>(null);
  useEffect(() => {
    if (prevWsRef.current && wsId && wsId !== prevWsRef.current) {
      useMyIssuesViewStore.getState().clearFilters();
    }
    prevWsRef.current = wsId ?? null;
  }, [wsId]);

  // Agents are only needed to construct the `agents` scope filter, but we
  // fetch unconditionally (it's cheap and TanStack Query dedupes) so the
  // user can switch tabs without an extra round-trip.
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const myAgents = useMemo<Agent[]>(
    () =>
      userId ? agents.filter((a) => a.owner_id === userId) : [],
    [agents, userId],
  );

  const filter = useMemo(
    () => (userId ? buildMyIssuesFilter(scope, userId, agents) : { assignee_id: "" }),
    [scope, userId, agents],
  );

  // myIssueListOptions internally disables fetch for the empty-agents case;
  // the outer `enabled` covers the no-user / no-workspace gate.
  const { data, isLoading, error, refetch, isRefetching } = useQuery({
    ...myIssueListOptions(wsId, scope, filter),
    enabled: !!wsId && !!userId,
  });

  // Apply client-side status + priority filter. Mirrors the predicate at
  // packages/views/issues/utils/filter.ts:30-34 via filterMyIssues().
  const filtered = useMemo(
    () => filterMyIssues(data ?? [], statusFilters, priorityFilters),
    [data, statusFilters, priorityFilters],
  );

  // When statusFilters is non-empty, intersect visible status order with it
  // so hidden statuses don't render an empty section header. Mirrors
  // packages/views/my-issues/components/my-issues-page.tsx:94-98.
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

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader title="My Issues" right={<HeaderActions />} />
      <ScopeTabs
        scope={scope}
        onChange={setScope}
        filterSlot={
          <FilterButton
            hasActive={hasActiveFilters}
            onPress={() => setSheetOpen(true)}
          />
        }
      />
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
      {scope === "agents" && myAgents.length === 0 ? (
        <EmptyState message="You don't have any agents yet." />
      ) : isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : error ? (
        <View className="px-4 gap-3">
          <Text className="text-sm text-destructive">
            Failed to load issues:{" "}
            {error instanceof Error ? error.message : "unknown error"}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            Retry
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

      <MyIssuesFilterSheet
        visible={sheetOpen}
        onClose={() => setSheetOpen(false)}
      />
    </SafeAreaView>
  );
}

function ScopeTabs({
  scope,
  onChange,
  filterSlot,
}: {
  scope: MyIssuesScope;
  onChange: (next: MyIssuesScope) => void;
  filterSlot?: React.ReactNode;
}) {
  return (
    <View className="flex-row items-center px-4 pb-2">
      <View className="flex-row gap-1 flex-1">
        {SCOPES.map((s) => {
          const active = s.value === scope;
          return (
            <Pressable
              key={s.value}
              onPress={() => onChange(s.value)}
              className={cn(
                "px-3 py-1.5 rounded-full",
                active ? "bg-secondary" : "active:bg-secondary/40",
              )}
            >
              <Text
                className={cn(
                  "text-sm",
                  active
                    ? "text-foreground font-medium"
                    : "text-muted-foreground",
                )}
              >
                {s.label}
              </Text>
            </Pressable>
          );
        })}
      </View>
      {filterSlot ? <View className="ml-2">{filterSlot}</View> : null}
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
  return (
    <Pressable
      onPress={onClear}
      className="flex-row items-center gap-1 pl-2.5 pr-2 py-1 rounded-full border border-border bg-secondary/40 active:bg-secondary"
    >
      <Text className="text-xs text-foreground">{label}</Text>
      <Svg width={10} height={10} viewBox="0 0 10 10">
        <Line x1="2" y1="2" x2="8" y2="8" stroke="#71717a" strokeWidth="1.5" strokeLinecap="round" />
        <Line x1="8" y1="2" x2="2" y2="8" stroke="#71717a" strokeWidth="1.5" strokeLinecap="round" />
      </Svg>
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
      return "Your agents aren't working on anything yet.";
  }
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
        <PriorityIcon priority={issue.priority} />
        <Text className="text-xs text-muted-foreground shrink-0 w-16">
          {issue.identifier}
        </Text>
        <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
          {issue.title}
        </Text>
        {issue.assignee_type && issue.assignee_id ? (
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

function FilterButton({
  hasActive,
  onPress,
}: {
  hasActive: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      className="size-9 items-center justify-center rounded-md border border-border bg-background active:bg-secondary"
    >
      <Svg width={16} height={16} viewBox="0 0 16 16">
        {/* Mirrors muted-foreground (#71717a) — same hex used by status-icon */}
        <Line x1="2" y1="4" x2="14" y2="4" stroke="#71717a" strokeWidth="1.5" strokeLinecap="round" />
        <Line x1="4" y1="8" x2="12" y2="8" stroke="#71717a" strokeWidth="1.5" strokeLinecap="round" />
        <Line x1="6" y1="12" x2="10" y2="12" stroke="#71717a" strokeWidth="1.5" strokeLinecap="round" />
      </Svg>
      {hasActive ? (
        <View className="absolute top-1 right-1 size-1.5 rounded-full bg-brand" />
      ) : null}
    </Pressable>
  );
}
