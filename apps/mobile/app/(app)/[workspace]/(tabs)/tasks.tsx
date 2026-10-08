/**
 * Full-space Tasks tab (RUYI-344) — the bottom 「任务」 tab, sliced into
 * five quadrant TABs:
 *   全部      no server status filter; 7 category sections, 已取消 pinned last
 *             (with an active status multi-select, sections follow the
 *             check order instead — and survive a restart with it)
 *   待处理    status_categories = backlog,todo      (two sections)
 *   进行中    status_categories = in_progress,in_review (two sections)
 *   已阻塞    status_categories = blocked           (flat, no header)
 *   已完成    status_categories = done              (flat, no header)
 *
 * Status category, not status key, is the slicing unit so custom statuses
 * inherit their category's TAB and section (MUL-6457 rule).
 *
 * Filters (design spec §3): status multi-select only on 全部 — other TABs
 * show a locked "状态：{{tab}}（由页签决定）" hint in the sheet instead,
 * because the tab owns the status window server-side. Priority / mine
 * relations / assignee / creator / agent-running apply on every TAB.
 * TAB switches preserve stored statusFilters (they re-apply when the user
 * returns to 全部); clearFilters keeps the current TAB and sort.
 *
 * Data: one server query per TAB window (`status_categories` narrowing —
 * the RUYI-199 answer), sorted by the active sort key. ≥2 mine relations
 * checked → three relation queries + `mergeTaskIssues` union (the old
 * actionable pattern, generalized). 智能体执行中 restricts the window to
 * the live running-issue set from the agent task snapshot (`ids` param).
 *
 * Known window limit (stated in the delivery report): the server caps
 * list responses at 100 rows; like web, v1 renders that window one-shot.
 */
import { useEffect, useMemo } from "react";
import {
  Pressable,
  SectionList,
  ScrollView,
  View,
  useWindowDimensions,
} from "react-native";
import { useQuery } from "@tanstack/react-query";
import { useIsFocused } from "@react-navigation/native";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type {
  Agent,
  Issue,
  IssueStatusCategory,
  MemberWithUser,
  Squad,
} from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Header } from "@/components/ui/header";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { StatusIcon } from "@/components/ui/status-icon";
import { IssueRowInbox } from "@/components/issue/issue-row-inbox";
import { IssuesLoading } from "@/components/issue/issues-loading";
import {
  buildTaskListFilter,
  mergeTaskIssues,
  runningIssueIdsFromSnapshot,
  taskListOptions,
  TASK_TAB_CATEGORIES,
} from "@/data/queries/tasks";
import { agentTaskSnapshotOptions } from "@/data/queries/agent-task-snapshot";
import { memberListOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import { squadListOptions } from "@/data/queries/squads";
import type { TaskActorRef, TaskTab } from "@/data/stores/tasks-view-store";
import { useTasksViewStore } from "@/data/stores/tasks-view-store";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { shouldWrapTaskPills } from "@/lib/task-toolbar";
import {
  localizedStatusLabel,
  priorityLabel,
  statusLabel,
} from "@/lib/issue-status";
import { useIssueStatuses } from "@/lib/use-issue-statuses";
import {
  categoryOrderFromStatusFilters,
  groupIssuesByCategory,
} from "@/lib/group-issues-by-category";
import { deriveIssueActivityMap } from "@/lib/issue-agent-activity";
import { filterIssues } from "@/lib/filter-issues";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";
import type { TFunction } from "i18next";

const MINE_RELATIONS = ["assigned", "created", "involved"] as const;

export default function Tasks() {
  const isFocused = useIsFocused();
  const { t } = useT("issues");
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);

  const tab = useTasksViewStore((s) => s.tab);
  const setTab = useTasksViewStore((s) => s.setTab);
  const sortBy = useTasksViewStore((s) => s.sortBy);
  const statusFilters = useTasksViewStore((s) => s.statusFilters);
  const priorityFilters = useTasksViewStore((s) => s.priorityFilters);
  const mineRelations = useTasksViewStore((s) => s.mineRelations);
  const assigneeRefs = useTasksViewStore((s) => s.assigneeRefs);
  const includeNoAssignee = useTasksViewStore((s) => s.includeNoAssignee);
  const creatorRefs = useTasksViewStore((s) => s.creatorRefs);
  const agentRunning = useTasksViewStore((s) => s.agentRunning);

  // Workspace-scoped filters live in a module-global store while this screen
  // remounts per workspace — and switch-workspace writes the new id before
  // the new screen mounts, so a ref-guard hook skips the transition. The
  // owning wsId is tracked inside the store; a real switch swaps to the
  // target workspace's own remembered set (empty when it has none) and keeps
  // TAB/sort (RUYI-531: 按空间记忆筛选，切回无需重选).
  useEffect(() => {
    useTasksViewStore.getState().syncWorkspace(wsId);
  }, [wsId]);

  // 模块级常量会在 i18n 初始化前固化（切语言不重算），页签 label 在
  // 组件内跟 t 一起算——同 my-issues scope pills 的处理。
  const tabs = useMemo<TaskTab[]>(
    () => ["all", "open", "active", "blocked", "completed"],
    [],
  );
  const tabLabel = (v: TaskTab) => t(`mobile.tasks.tabs.${v}`);

  const openFilter = () => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/issues-filter",
      params: { workspace: wsSlug, scope: "tasks" },
    });
  };
  const openSort = () => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/tasks-sort",
      params: { workspace: wsSlug },
    });
  };

  // ── Agent-task snapshot ───────────────────────────────────────────
  // Always on (RUYI-413): the per-row running/queued badge derives from the
  // same workspace snapshot the inbox tab uses (warmed at workspace entry,
  // kept fresh by use-presence-realtime — no new fetch on the hot path),
  // and the 智能体执行中 filter still reads its running-issue set from it.
  // Toggle on + empty set = nothing is running → skip the list queries and
  // render the empty state.
  const snapshotQuery = useQuery({
    ...agentTaskSnapshotOptions(wsId),
    enabled: !!wsId,
  });
  // One derivation pass per render for the whole list — the inbox screen's
  // pattern, not one per row.
  const activityByIssue = useMemo(
    () => deriveIssueActivityMap(snapshotQuery.data ?? []),
    [snapshotQuery.data],
  );
  const runningIssueIds = useMemo(
    () =>
      agentRunning
        ? runningIssueIdsFromSnapshot(snapshotQuery.data ?? [])
        : undefined,
    [agentRunning, snapshotQuery.data],
  );
  // Toggle on + snapshot loaded + no live issue = nothing to list; skip the
  // queries entirely (the mobile API drops empty arrays, and the server's
  // `ids` window would be empty anyway) and render the filtered empty state.
  const runningWindowBlocked =
    agentRunning &&
    !snapshotQuery.isLoading &&
    (runningIssueIds?.length ?? 0) === 0;

  // Store arrays/objects are stable references between renders (zustand
  // hands back the same instance until a write), so they're safe memo deps.
  const mineActive = useMemo(
    () => MINE_RELATIONS.filter((k) => mineRelations[k]),
    [mineRelations],
  );
  const unionMode = mineActive.length >= 2;

  // ── Queries ────────────────────────────────────────────────────────
  // ≤1 mine relation → one query carrying the whole filter. ≥2 → three
  // relation queries (same wire shape) + client-side union. agentRunning
  // with an empty running set disables every list query.
  const listEnabled =
    !!wsId &&
    !runningWindowBlocked &&
    (!agentRunning || !snapshotQuery.isLoading);
  const singleFilter = useMemo(
    () =>
      buildTaskListFilter({
        tab,
        sortBy,
        userId,
        assigneeRefs,
        includeNoAssignee,
        creatorRefs,
        runningIssueIds,
        mine: mineActive.length === 1 ? mineActive[0] : undefined,
      }),
    [tab, sortBy, userId, assigneeRefs, includeNoAssignee, creatorRefs,
      runningIssueIds, mineActive],
  );

  const unionFilters = useMemo(
    () =>
      MINE_RELATIONS.map((relation) =>
        buildTaskListFilter({
          tab,
          sortBy,
          userId,
          assigneeRefs,
          includeNoAssignee,
          creatorRefs,
          runningIssueIds,
          mine: relation,
        }),
      ),
    [tab, sortBy, userId, assigneeRefs, includeNoAssignee, creatorRefs,
      runningIssueIds],
  );

  const singleQuery = useQuery({
    ...taskListOptions(wsId, singleFilter),
    enabled: listEnabled && !unionMode,
  });
  const assignedQuery = useQuery({
    ...taskListOptions(wsId, unionFilters[0]),
    enabled: listEnabled && unionMode,
  });
  const createdQuery = useQuery({
    ...taskListOptions(wsId, unionFilters[1]),
    enabled: listEnabled && unionMode,
  });
  const involvedQuery = useQuery({
    ...taskListOptions(wsId, unionFilters[2]),
    enabled: listEnabled && unionMode,
  });

  const data: Issue[] | undefined = useMemo(() => {
    if (unionMode) {
      if (!assignedQuery.data || !createdQuery.data || !involvedQuery.data) {
        return undefined; // still loading — render the loading state
      }
      return mergeTaskIssues(
        {
          assigned: assignedQuery.data,
          created: createdQuery.data,
          involved: involvedQuery.data,
        },
        sortBy,
      );
    }
    return singleQuery.data;
  }, [
    unionMode,
    assignedQuery.data,
    createdQuery.data,
    involvedQuery.data,
    singleQuery.data,
    sortBy,
  ]);

  // runningWindowBlocked → list queries are disabled on purpose; report
  // "not loading" so the filtered empty state renders instead of a spinner.
  const isLoading = runningWindowBlocked
    ? false
    : unionMode
      ? !assignedQuery.data || !createdQuery.data || !involvedQuery.data
      : singleQuery.isLoading;
  const error = unionMode
    ? (assignedQuery.error ?? createdQuery.error ?? involvedQuery.error)
    : (snapshotQuery.error ?? singleQuery.error);
  const refetch = () => {
    snapshotQuery.refetch();
    if (unionMode) {
      assignedQuery.refetch();
      createdQuery.refetch();
      involvedQuery.refetch();
    } else {
      singleQuery.refetch();
    }
  };
  const isRefetching = unionMode
    ? assignedQuery.isRefetching || createdQuery.isRefetching || involvedQuery.isRefetching
    : singleQuery.isRefetching || snapshotQuery.isRefetching;

  // Only the active-filter chips need the catalog: sections group on the
  // category the server already resolved onto each issue. (MUL-6243)
  const catalog = useIssueStatuses();

  // Status multi-select applies on 全部 only — every other TAB's status
  // window is owned by the tab itself (the sheet shows the locked hint).
  // Stored statusFilters survive TAB switches and re-apply on 全部.
  const filtered = useMemo(
    () =>
      filterIssues(
        data ?? [],
        tab === "all" ? statusFilters : [],
        priorityFilters,
      ),
    [data, tab, statusFilters, priorityFilters],
  );

  // 全部 renders all seven category sections (已取消 pinned last);
  // 待处理/进行中 restrict the canonical sections to the tab's categories;
  // 已阻塞/已完成 render one flat section with no header.
  // With an active status multi-select, 全部 orders its sections by the
  // check order instead (RUYI-344 增量): each checked key resolves to its
  // category via the catalog (built-ins are their own category; a not-yet-
  // loaded catalog answers `todo`, and the order self-corrects once it
  // arrives). Unselected categories can only hold rows the key filter
  // already dropped, so this reorders exactly the visible sections; no
  // selection → canonical order, 已取消 still pinned last.
  const sections = useMemo(() => {
    if (tab === "blocked" || tab === "completed") {
      return [
        {
          category: TASK_TAB_CATEGORIES[tab]![0],
          data: filtered,
          flat: true,
        },
      ];
    }
    const categoryOrder =
      tab === "all" && statusFilters.length > 0
        ? categoryOrderFromStatusFilters(statusFilters, catalog.categoryOf)
        : undefined;
    const grouped = groupIssuesByCategory(filtered, {
      includeCancelled: tab === "all",
      categoryOrder,
    });
    const allowed = TASK_TAB_CATEGORIES[tab];
    return allowed
      ? grouped
          .filter((s) => allowed.includes(s.category))
          .map((s) => ({ ...s, flat: false }))
      : grouped.map((s) => ({ ...s, flat: false }));
  }, [filtered, tab, statusFilters, catalog]);

  const hasActiveFilters =
    (tab === "all" && statusFilters.length > 0) ||
    priorityFilters.length > 0 ||
    mineActive.length > 0 ||
    assigneeRefs.length > 0 ||
    includeNoAssignee ||
    creatorRefs.length > 0 ||
    agentRunning;

  const showEmptyState = !isLoading && !error && filtered.length === 0;

  // Actor lists feed the assignee/creator chip labels; they're cached from
  // the picker sheet in the common case. A chip renders once its name
  // resolves (no raw-uuid flash while lists load).
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const refName = (ref: TaskActorRef): string | null => {
    if (ref.type === "member") {
      return (
        (members as MemberWithUser[]).find((m) => m.user_id === ref.id)?.name ??
        null
      );
    }
    if (ref.type === "agent") {
      return (agents as Agent[]).find((a) => a.id === ref.id)?.name ?? null;
    }
    return (squads as Squad[]).find((s) => s.id === ref.id)?.name ?? null;
  };

  return (
    <View className="flex-1 bg-background">
      <Header
        title={t("mobile.tasks.page.title", "Tasks")}
        right={<HeaderActions />}
      />
      <TasksToolbar
        tabs={tabs}
        tab={tab}
        tabLabel={tabLabel}
        onChange={(v) => setTab(v)}
        onOpenSort={openSort}
        onOpenFilter={openFilter}
        sortActive={sortBy !== "updated_at"}
        filterActive={hasActiveFilters}
      />
      {hasActiveFilters ? (
        <View className="flex-row flex-wrap gap-1.5 px-4 pb-2">
          {tab === "all" &&
            statusFilters.map((s) => (
              <Chip
                key={`s-${s}`}
                label={localizedStatusLabel(catalog, s)}
                onClear={() =>
                  useTasksViewStore.getState().toggleStatusFilter(s)
                }
              />
            ))}
          {priorityFilters.map((p) => (
            <Chip
              key={`p-${p}`}
              label={priorityLabel(p)}
              onClear={() =>
                useTasksViewStore.getState().togglePriorityFilter(p)
              }
            />
          ))}
          {mineActive.map((k) => (
            <Chip
              key={`m-${k}`}
              label={t(`mobile.tasks.filters.mine_${k}`)}
              onClear={() =>
                useTasksViewStore.getState().toggleMineRelation(k)
              }
            />
          ))}
          {assigneeRefs.map((ref) =>
            refName(ref) ? (
              <Chip
                key={`a-${ref.type}-${ref.id}`}
                label={refName(ref)!}
                onClear={() =>
                  useTasksViewStore.getState().setAssigneeRefs(
                    assigneeRefs.filter(
                      (r) => !(r.type === ref.type && r.id === ref.id),
                    ),
                  )
                }
              />
            ) : null,
          )}
          {includeNoAssignee ? (
            <Chip
              key="a-none"
              label={t("filters.no_assignee", "No assignee")}
              onClear={() =>
                useTasksViewStore.getState().setIncludeNoAssignee(false)
              }
            />
          ) : null}
          {creatorRefs.map((ref) =>
            refName(ref) ? (
              <Chip
                key={`c-${ref.type}-${ref.id}`}
                label={refName(ref)!}
                onClear={() =>
                  useTasksViewStore.getState().setCreatorRefs(
                    creatorRefs.filter(
                      (r) => !(r.type === ref.type && r.id === ref.id),
                    ),
                  )
                }
              />
            ) : null,
          )}
          {agentRunning ? (
            <Chip
              key="agent"
              label={t("mobile.tasks.filters.agent_running", "Agent running")}
              onClear={() =>
                useTasksViewStore.getState().toggleAgentRunning()
              }
            />
          ) : null}
        </View>
      ) : null}
      {isLoading ? (
        <IssuesLoading />
      ) : error ? (
        <View className="px-4 gap-3 pt-4">
          <Text className="text-sm text-destructive">
            {/* 整句插值，不做「失败：」+ 详情的拼接——中日韩里详情的
                位置与英文不同。与 my-issues / issue 详情同一处理。 */}
            {t("my-issues:mobile.page.load_failed", "Failed to load issues: {{reason}}", {
              reason:
                error instanceof Error
                  ? error.message
                  : t("common:mobile.common.unknown_error", "unknown error"),
            })}
          </Text>
          <Button variant="outline" onPress={refetch}>
            <Text>{t("common:mobile.common.retry", "Retry")}</Text>
          </Button>
        </View>
      ) : showEmptyState ? (
        <TasksEmptyState
          hasActiveFilters={hasActiveFilters}
          tab={tab}
          tabLabel={tabLabel}
          t={t}
        />
      ) : (
        <SectionList
          sections={sections}
          keyExtractor={(item) => item.id}
          stickySectionHeadersEnabled={false}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-4" />
          )}
          renderSectionHeader={({ section }) =>
            section.flat ? null : (
              <SectionHeader category={section.category} count={section.data.length} />
            )
          }
          contentContainerClassName="pb-6"
          renderItem={({ item }) => (
            <IssueRowInbox
              issue={item}
              activity={activityByIssue.get(item.id)}
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
 * Toolbar mirroring my-issues' ScopeToolbar: horizontally scrolling TAB
 * pills + fixed sort / filter icon buttons on the right (both show a red
 * dot when non-default). Five pills don't fit a 375pt row even without
 * icons, so the pill row scrolls like the four-scope row it replaces —
 * except above fontScale 1, where the row becomes an adaptive wrapping
 * flow instead: the drag response of the scroller proved dead on device
 * with pills clipped off (item 10, P2), so every TAB must stay visible
 * and tappable without relying on a gesture.
 */
function TasksToolbar({
  tabs,
  tab,
  tabLabel,
  onChange,
  onOpenSort,
  onOpenFilter,
  sortActive,
  filterActive,
}: {
  tabs: TaskTab[];
  tab: TaskTab;
  tabLabel: (v: TaskTab) => string;
  onChange: (value: TaskTab) => void;
  onOpenSort: () => void;
  onOpenFilter: () => void;
  sortActive: boolean;
  filterActive: boolean;
}) {
  const { t } = useT("issues");
  const wrapPills = shouldWrapTaskPills(useWindowDimensions().fontScale);
  const pills = tabs.map((v) => {
    const active = tab === v;
    return (
      <Button
        key={v}
        variant="outline"
        size="sm"
        onPress={() => onChange(v)}
        className={active ? "bg-accent" : ""}
        accessibilityState={{ selected: active }}
      >
        <Text
          numberOfLines={1}
          className={active ? "text-accent-foreground" : "text-muted-foreground"}
        >
          {tabLabel(v)}
        </Text>
      </Button>
    );
  });
  return (
    <View className="flex-row items-center px-4 pt-2 pb-2">
      {wrapPills ? (
        <View className="flex-1 flex-row flex-wrap items-center gap-1">
          {pills}
        </View>
      ) : (
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          className="flex-1"
          contentContainerClassName="flex-row items-center gap-1"
        >
          {pills}
        </ScrollView>
      )}
      <ToolbarIconButton
        onPress={onOpenSort}
        active={sortActive}
        icon="swap-vertical-outline"
        accessibilityLabel={t("mobile.tasks.sort.a11y", "Sort")}
      />
      <ToolbarIconButton
        onPress={onOpenFilter}
        active={filterActive}
        icon="options-outline"
        accessibilityLabel={t("filters.tooltip", "Filter")}
        extraLeftMargin
      />
    </View>
  );
}

function ToolbarIconButton({
  onPress,
  active,
  icon,
  accessibilityLabel,
  extraLeftMargin,
}: {
  onPress: () => void;
  active: boolean;
  icon: keyof typeof Ionicons.glyphMap;
  accessibilityLabel: string;
  extraLeftMargin?: boolean;
}) {
  const { colorScheme } = useColorScheme();
  return (
    <View
      style={{ position: "relative" }}
      className={extraLeftMargin ? "ml-2" : "ml-1.5"}
    >
      <Button
        variant="outline"
        size="sm"
        onPress={onPress}
        accessibilityLabel={accessibilityLabel}
        className="w-9 px-0"
      >
        <Ionicons
          name={icon}
          size={16}
          color={THEME[colorScheme].mutedForeground}
        />
      </Button>
      {active ? (
        <View
          pointerEvents="none"
          className="absolute top-1 right-1 size-1.5 rounded-full bg-brand"
        />
      ) : null}
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

function TasksEmptyState({
  hasActiveFilters,
  tab,
  tabLabel,
  t,
}: {
  hasActiveFilters: boolean;
  tab: TaskTab;
  tabLabel: (v: TaskTab) => string;
  t: TFunction;
}) {
  return (
    <View className="flex-1 items-center justify-center px-6 gap-3">
      <Text className="text-sm text-muted-foreground text-center">
        {hasActiveFilters
          ? t("mobile.tasks.filtered_empty.title", "No issues match these filters")
          : t(`mobile.tasks.empty.${tab}`, "Nothing in {{tab}} yet.", {
              tab: tabLabel(tab),
            })}
      </Text>
      {hasActiveFilters ? (
        <Button
          variant="outline"
          onPress={() => useTasksViewStore.getState().clearFilters()}
        >
          <Text>{t("mobile.tasks.filtered_empty.clear_button", "Clear filters")}</Text>
        </Button>
      ) : null}
    </View>
  );
}
