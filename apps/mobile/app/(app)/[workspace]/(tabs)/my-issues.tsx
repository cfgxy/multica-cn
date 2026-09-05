/**
 * "My Issues" tab. Scopes mirror web's
 * `packages/views/my-issues/components/my-issues-page.tsx:48-65` —
 * assigned / created / agents — plus the mobile-only merged
 * `actionable`（待我推进）scope (RUYI-76 ①): the client-side union of the
 * three server relations restricted to the four action categories
 * (待规划/待办/进行中/待审核). It exists because the `assigned` scope filters
 * `assignee_id = <user>` server-side, and in squad-driven workspaces issues
 * carry the SQUAD's UUID, so the personal assigned list is legitimately
 * empty most days and read as "the tab is broken". Merge semantics:
 * `lib/my-actionable-issues.ts` (dedupe by id, category-restricted,
 * server position order). The `agents` scope label is "Agents" because the
 * backend predicate (`involves_user_id`, MUL-2397) surfaces both the user's
 * owned agents and squads they're involved in.
 *
 * Issues are grouped by status CATEGORY using SectionList in
 * `BOARD_CATEGORIES` order; empty sections are filtered out so the screen
 * doesn't fill with "(0)" headers. Grouping is by category, not by status key,
 * because a workspace's custom statuses live inside their category's section
 * rather than adding one of their own — bucketing by key is what made
 * custom-status issues disappear from this list (MUL-6457). `cancelled` stays
 * excluded, so a custom status in that category is hidden here exactly like the
 * built-in Cancelled is: a custom status inherits its category's behavior.
 *
 * Status + Priority filters mirror web's MyIssuesHeader filter sub-menus.
 * Filter state lives in `useMyIssuesViewStore` and is cleared on workspace
 * change via the shared `useClearFiltersOnWorkspaceChange` hook.
 */
import { useMemo } from "react";
import { Pressable, SectionList, ScrollView, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { useIsFocused } from "@react-navigation/native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type {
  IssuePriority,
  IssueStatus,
  IssueStatusCategory,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Header } from "@/components/ui/header";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { StatusIcon } from "@/components/ui/status-icon";
import { IssueRow } from "@/components/issue/issue-row";
import { IssuesLoading } from "@/components/issue/issues-loading";
import {
  buildMyIssuesFilter,
  myIssueListOptions,
  myScopeFilters,
} from "@/data/queries/my-issues";
import type {
  MyIssuesFilter,
  MyIssuesScope,
} from "@/data/queries/issue-keys";
import { buildActionableIssues } from "@/lib/my-actionable-issues";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useMyIssuesViewStore } from "@/data/stores/my-issues-view-store";
import { useClearFiltersOnWorkspaceChange } from "@/lib/use-clear-filters-on-workspace-change";
import {
  localizedStatusLabel,
  priorityLabel,
  statusLabel,
} from "@/lib/issue-status";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import { groupIssuesByCategory } from "@/lib/group-issues-by-category";
import { filterIssues } from "@/lib/filter-issues";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import type { TFunction } from "i18next";
import { useT } from "@/lib/use-t";

// Mobile pill row has tight width on SE3 (375pt). Four pills + Filter icon
// cannot fit in 343pt usable space (the "待我推进" addition pushed three
// past the limit), so the pill row scrolls horizontally — semantics
// unchanged. The agents scope renders "Agents": the full "Agents and
// Squads" label (~135pt) breaks under Dynamic Type. Semantics unchanged:
// same backend predicate (`involves_user_id`, MUL-2397) covers owned
// agents + related squads; the empty state copy still says "agents or
// squads".
// 两侧的模块级常量都已无引用：分组交给上游的 `groupIssuesByCategory`
// （不再需要 `IssueSection`），scope 列表移进组件内跟 t 一起重算
// （模块顶层的 `SCOPES` 会在 i18n 初始化前固化，切语言不重算）。

export default function MyIssues() {
  const isFocused = useIsFocused();
  const { t } = useT("my-issues");
  // 模块级常量会在 i18n 初始化之前求值一次就固化，切语言不重算——所以
  // 放进组件里跟 t 一起重算（同 apps/mobile/CLAUDE.md 的 i18n 规则）。
  // 待我推进排第一：它是本 tab 的默认 scope（RUYI-76 ①）。
  const scopes = useMemo<{ value: MyIssuesScope; label: string }[]>(
    () => [
      {
        value: "actionable",
        label: t("header.scope.actionable_label", "Actionable"),
      },
      { value: "assigned", label: t("header.scope.assigned_label", "Assigned") },
      { value: "created", label: t("header.scope.created_label", "Created") },
      { value: "agents", label: t("issues:scope.agents_label", "Agents") },
    ],
    [t],
  );
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

  const isActionable = scope === "actionable";

  // Single-relation scopes keep their one server-filtered query; the merged
  // actionable scope mounts all three relation queries (same cache keys the
  // single scopes use) and unions them client-side.
  const singleFilter: MyIssuesFilter | null = useMemo(
    () =>
      scope !== "actionable" && userId
        ? buildMyIssuesFilter(scope, userId)
        : null,
    [scope, userId],
  );
  const relationFilters = useMemo(
    () => (userId ? myScopeFilters(userId) : null),
    [userId],
  );

  const singleQuery = useQuery({
    ...myIssueListOptions(
      wsId,
      // singleFilter is non-null exactly when !isActionable, so scope is a
      // single-relation scope here.
      scope as Exclude<MyIssuesScope, "actionable">,
      singleFilter ?? { assignee_id: "" },
    ),
    enabled: !!wsId && !!userId && !isActionable,
  });
  const assignedQuery = useQuery({
    ...myIssueListOptions(wsId, "assigned", relationFilters?.assigned ?? { assignee_id: "" }),
    enabled: !!wsId && !!userId && isActionable,
  });
  const createdQuery = useQuery({
    ...myIssueListOptions(wsId, "created", relationFilters?.created ?? { creator_id: "" }),
    enabled: !!wsId && !!userId && isActionable,
  });
  const involvedQuery = useQuery({
    ...myIssueListOptions(wsId, "agents", relationFilters?.agents ?? { involves_user_id: "" }),
    enabled: !!wsId && !!userId && isActionable,
  });

  const data = useMemo(() => {
    if (!isActionable) return singleQuery.data;
    if (
      !assignedQuery.data ||
      !createdQuery.data ||
      !involvedQuery.data
    ) {
      return undefined; // still loading — render the loading state
    }
    return buildActionableIssues({
      assigned: assignedQuery.data,
      created: createdQuery.data,
      involved: involvedQuery.data,
    });
  }, [
    isActionable,
    singleQuery.data,
    assignedQuery.data,
    createdQuery.data,
    involvedQuery.data,
  ]);

  const isLoading = isActionable
    ? !assignedQuery.data || !createdQuery.data || !involvedQuery.data
    : singleQuery.isLoading;
  const error = isActionable
    ? (assignedQuery.error ?? createdQuery.error ?? involvedQuery.error)
    : singleQuery.error;
  const refetch = isActionable
    ? () => {
        assignedQuery.refetch();
        createdQuery.refetch();
        involvedQuery.refetch();
      }
    : singleQuery.refetch;
  const isRefetching = isActionable
    ? assignedQuery.isRefetching ||
      createdQuery.isRefetching ||
      involvedQuery.isRefetching
    : singleQuery.isRefetching;

  // Only the active-filter chips need the catalog: sections group on the
  // category the server already resolved onto each issue, so the list never
  // waits for this. (MUL-6243)
  const catalog = useIssueStatuses();

  // Apply client-side status + priority filter. Mirrors the predicate at
  // packages/views/issues/utils/filter.ts:30-34 via filterIssues().
  const filtered = useMemo(
    () => filterIssues(data ?? [], statusFilters, priorityFilters),
    [data, statusFilters, priorityFilters],
  );

  const sections = useMemo(() => groupIssuesByCategory(filtered), [filtered]);

  const hasActiveFilters =
    statusFilters.length > 0 || priorityFilters.length > 0;

  const showEmptyState =
    !isLoading && !error && filtered.length === 0;

  return (
    <View className="flex-1 bg-background">
      <Header
        title={t("mobile.page.title", "My Issues")}
        right={<HeaderActions />}
      />
      <ScopeToolbar
        scopes={scopes}
        scope={scope}
        onChange={(v) => setScope(v)}
        onOpenFilter={openFilter}
        hasActiveFilters={hasActiveFilters}
      />
      {hasActiveFilters ? (
        <ActiveFilterChips
          statusFilters={statusFilters}
          priorityFilters={priorityFilters}
          statusLabelOf={(s) => localizedStatusLabel(catalog, s)}
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
            {/* 整句插值，不做「失败：」+ 详情的拼接——中日韩里详情的
                位置与英文不同。与 select-workspace / issue 详情同一处理。 */}
            {t("mobile.page.load_failed", "Failed to load issues: {{reason}}", {
              reason:
                error instanceof Error
                  ? error.message
                  : t("common:mobile.common.unknown_error", "unknown error"),
            })}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            <Text>{t("common:mobile.common.retry", "Retry")}</Text>
          </Button>
        </View>
      ) : showEmptyState ? (
        <EmptyState
          message={
            hasActiveFilters
              ? t("mobile.empty.filtered", "No issues match the current filters.")
              : emptyMessageForScope(scope, t)
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
              category={section.category}
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
          refreshing={isFocused && isRefetching}
          onRefresh={refetch}
        />
      )}

    </View>
  );
}

/**
 * Outline icon button matching the pill height so the toolbar row reads as
 * one visual group. Mirrors web `IssuesHeader` / `MyIssuesHeader` filter
 * trigger (`packages/views/my-issues/components/my-issues-header.tsx:174`),
 * which is also `variant="outline"` + icon-sized — NOT the ghost-style we'd
 * get from <IconButton>. Square (`w-9`) with `px-0` to suppress the sm
 * default `px-3`.
 */
function FilterButton({
  onPress,
  hasActiveFilters,
}: {
  onPress: () => void;
  hasActiveFilters: boolean;
}) {
  const { colorScheme } = useColorScheme();
  const { t } = useT("my-issues");
  return (
    <View style={{ position: "relative" }} className="ml-2">
      <Button
        variant="outline"
        size="sm"
        onPress={onPress}
        accessibilityLabel={t("mobile.page.filter_a11y", "Filter")}
        className="w-9 px-0"
      >
        <Ionicons
          name="options-outline"
          size={16}
          color={THEME[colorScheme].mutedForeground}
        />
      </Button>
      {hasActiveFilters ? (
        <View
          pointerEvents="none"
          className="absolute top-1 right-1 size-1.5 rounded-full bg-brand"
        />
      ) : null}
    </View>
  );
}

/**
 * Toolbar row mirroring web `MyIssuesHeader` / `IssuesHeader`
 * (`packages/views/my-issues/components/my-issues-header.tsx:138-163`):
 * left-aligned scope pill group + right-side Filter icon (red dot when
 * filters are active). Replaces the previous full-width segmented tabs +
 * Filter-in-title-bar split — keeps scope and the filter affordance in the
 * same row, because they both control the list directly below.
 *
 * The pill group scrolls horizontally since RUYI-76 ① added the fourth
 * (待我推进) scope — four pills + the Filter icon no longer fit a 375pt
 * (SE3) row, and clipping a scope the user can't see is worse than a scroll.
 */
function ScopeToolbar<S extends string>({
  scopes,
  scope,
  onChange,
  onOpenFilter,
  hasActiveFilters,
}: {
  scopes: { value: S; label: string }[];
  scope: S;
  onChange: (value: S) => void;
  onOpenFilter: () => void;
  hasActiveFilters: boolean;
}) {
  return (
    <View className="flex-row items-center px-4 pt-2 pb-2">
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        className="flex-1"
        contentContainerClassName="flex-row items-center gap-1"
      >
        {scopes.map((s) => {
          const active = scope === s.value;
          return (
            <Button
              key={s.value}
              variant="outline"
              size="sm"
              onPress={() => onChange(s.value)}
              className={active ? "bg-accent" : ""}
              accessibilityState={{ selected: active }}
            >
              <Text
                numberOfLines={1}
                className={active ? "text-accent-foreground" : "text-muted-foreground"}
              >
                {s.label}
              </Text>
            </Button>
          );
        })}
      </ScrollView>
      <FilterButton
        onPress={onOpenFilter}
        hasActiveFilters={hasActiveFilters}
      />
    </View>
  );
}

function ActiveFilterChips({
  statusFilters,
  priorityFilters,
  statusLabelOf,
  onClearStatus,
  onClearPriority,
}: {
  statusFilters: IssueStatus[];
  priorityFilters: IssuePriority[];
  /** Resolves a status KEY — which can be a custom one — to its label. */
  statusLabelOf: (statusKey: string) => string;
  onClearStatus: (s: IssueStatus) => void;
  onClearPriority: (p: IssuePriority) => void;
}) {
  return (
    <View className="flex-row flex-wrap gap-1.5 px-4 pb-2">
      {statusFilters.map((s) => (
        <Chip key={`s-${s}`} label={statusLabelOf(s)} onClear={() => onClearStatus(s)} />
      ))}
      {priorityFilters.map((p) => (
        <Chip key={`p-${p}`} label={priorityLabel(p)} onClear={() => onClearPriority(p)} />
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

// The header names the CATEGORY, not any one status inside it, so it keeps
// mobile's own copy and its category glyph even when the section holds custom
// statuses.
function SectionHeader({
  category,
  count,
}: {
  category: IssueStatusCategory;
  count: number;
}) {
  return (
    <View className="flex-row items-center gap-2 px-4 py-2 bg-background">
      {/* A category IS a built-in status key, so it resolves to its own glyph. */}
      <StatusIcon status={category} size={14} />
      <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
        {/* category 本身就是内置状态键，`statusLabel` 走 `issues:status.*`。 */}
        {statusLabel(category)}
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

function emptyMessageForScope(
  scope: MyIssuesScope,
  t: TFunction,
): string {
  switch (scope) {
    case "actionable":
      // 待规划/待办/进行中/待审核四个类别下都没有等待「我」的 issue。
      return t(
        "mobile.empty.actionable",
        "Nothing is waiting on you right now.",
      );
    case "assigned":
      return t("mobile.empty.assigned", "No issues assigned to you.");
    case "created":
      return t("mobile.empty.created", "You haven't created any issues.");
    case "agents":
      return t(
        "mobile.empty.agents",
        "No issues assigned to your agents or squads yet.",
      );
  }
}
