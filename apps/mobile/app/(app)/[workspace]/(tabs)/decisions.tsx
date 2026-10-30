/**
 * 决策中心 (RUYI-494/530, refolded by RUYI-864) — workspace-level decision
 * card aggregation, the mobile home of the bottom 「决策」 tab.
 *
 * The screen keeps only the decision list — the authorization queue above it
 * is now a single card (the server's oldest pending row), and the whole
 * card list is keyset-paged. One row per CARD, never folded by issue
 * (Owner IA on RUYI-494): line 1 = issue identifier + issue title
 * (+ 「有推荐」 badge), line 2 = that card's own question + time-ago; the
 * left column carries the card creator's avatar (RUYI-530). Decided rows
 * (answered/cancelled) render the inbox's read style — muted title, faded
 * secondary — open rows keep full contrast.
 *
 * RUYI-864 refold (three-end parity, web's decision-center-page.tsx is the
 * source of truth): the status TAB pills are a single-value window
 * selector that travels to the server as the `status` param
 * (全部 → no param; 待决策/已决策/已失效 → one card status, 1:1), and a
 * debounced keyword search travels as `q` — the list, the section counts
 * and every empty state all follow the SAME filter window, so they can
 * never disagree. The client-side 有推荐 / Agent 发起 sheet toggles and the
 * decisions-filter formSheet are gone: a client-side row drop could not
 * stay honest to the server counts once the list is paged. The
 * authorization section is the pending queue (order=asc, limit=1 — the
 * server hands back exactly the oldest pending row, so approving one
 * auto-advances the next and the area vanishes when the queue empties,
 * with no client queue state) plus a low-key collapsed history entry.
 * Tap → the card's issue with `decision` + `h` params, the same re-tap
 * nonce idiom the inbox deep-link uses.
 */
import { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  SectionList,
  ScrollView,
  View,
  useWindowDimensions,
  type SectionListRenderItemInfo,
} from "react-native";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Header } from "@/components/ui/header";
import { TextField } from "@/components/ui/text-field";
import { DecisionInboxRow } from "@/components/decision/decision-inbox-row";
import { DecisionRequestRow } from "@/components/decision/decision-request-row";
import {
  workspaceDecisionInboxPageOptions,
  workspaceDecisionRequestsOptions,
  workspaceDecisionRequestsHistoryPageOptions,
} from "@/data/queries/decisions";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useDecisionsViewStore } from "@/data/stores/decisions-view-store";
import type { DecisionRequest } from "@multica/core/types";
import {
  DECISION_TAB_ORDER,
  groupDecisionInboxSections,
  type DecisionInboxRow as DecisionRowData,
  type DecisionTab,
} from "@/lib/decision-inbox-display";
import { shouldWrapTaskPills } from "@/lib/task-toolbar";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

/**
 * RN SectionList's `sections` element: `data` is the prop name the list
 * wants, but the shared `groupDecisionInboxSections` emits `rows` — the map
 * lives at the call site and the resulting element type is what
 * `SectionList`'s section generic rides on. One local type, two call sites
 * (`sections` + `renderItem`); no new generic primitive.
 */
type DecisionsSection = {
  key: DecisionRowData["status"];
  count: number;
  data: DecisionRowData[];
};

/**
 * RUYI-864: 250ms debounce on the search keyword. A stable `q` is what the
 * infinite query's key rides on, so debouncing settles the request stream
 * instead of firing one refetch per keystroke. (React 19's
 * `useDeferredValue` defers render priority, not request timing — a timeout
 * debounce is what search.tsx already uses for its API calls; this is the
 * same idea applied to query-key stability.) Single-caller helper, kept
 * local to the screen per the mobile "no new generic primitive" rule.
 */
function useDebouncedValue<T>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return debounced;
}

export default function Decisions() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("decisions");

  // The status window (RUYI-864): the TAB pill is a single-value window
  // selector — 全部 maps to no `status` param, the three card statuses map
  // 1:1 to the server's `status` param.
  const tab = useDecisionsViewStore((s) => s.tab);
  const status = tab === "all" ? undefined : tab;

  // Session-scoped keyword: a stale search string on the next visit is
  // noise, so it stays in local state, not the persisted store.
  const [searchInput, setSearchInput] = useState("");
  const searchQ = useDebouncedValue(searchInput.trim(), 250);
  const q = searchQ || undefined;
  const hasWindow = status !== undefined || q !== undefined;

  // The decision card list, keyset-paged: the server walks (created_at, id)
  // under the filter window and "Load more" fetches the next page.
  const pageQuery = useInfiniteQuery(
    workspaceDecisionInboxPageOptions(wsId ?? null, { status, q }),
  );
  const pages = pageQuery.data?.pages ?? [];
  const items = useMemo(() => pages.flatMap((page) => page.items), [pages]);
  // Section grouping + the window-scoped counts (from the last page) ride on
  // the walked pages; a zero-row section is dropped, but a section's count is
  // the server's filter-window total, not rows.length — so headers stay true
  // after paging. RN SectionList wants `data`, so map the `rows` onto it.
  const sections: DecisionsSection[] =
    useMemo(
      () =>
        groupDecisionInboxSections(pages).map((s) => ({
          key: s.key,
          count: s.count,
          data: s.rows,
        })),
      [pages],
    );

  // Workspace-scoped view state lives in a module-global store while this
  // screen remounts per workspace — same syncWorkspace pattern as the Tasks
  // tab (the switch writes the new id before the remounted screen syncs).
  useEffect(() => {
    useDecisionsViewStore.getState().syncWorkspace(wsId);
  }, [wsId]);

  // The pending authorization queue (RUYI-864): one row, the server's
  // oldest pending (order=asc, limit=1). Approving/denying invalidates the
  // whole request subtree (DecisionRequestRow's refresh), so the next oldest
  // row slides in and the area vanishes when none is left — no client
  // queue state. The row's operable flag carries the processing/validity
  // split (a read-only projection renders with its hint).
  const { data: requestsData, isPending: requestsPending } = useQuery(
    workspaceDecisionRequestsOptions(wsId ?? null, {
      status: "pending",
      order: "asc",
      limit: 1,
    }),
  );
  const pendingRow = requestsData?.items[0];
  // RUYI-864 rework: the entry's total is the server's `history` count —
  // every non-pending row (approved ∪ closed ∪ executed), the SAME set the
  // terminal-state window below walks. An "approved, waiting on the other
  // space" row keeps the entry visible even though the queue's own
  // status=pending call already stopped showing it — web's
  // decision-center-page.tsx drives the badge from the same field.
  const historyTotal = requestsData?.counts.history ?? 0;

  // The collapsed history entry: the server's terminal-state window
  // (status=history, newest-first), keyset-paged and lazily fetched on
  // open. Pending rows never show up here — the window excludes them at
  // the source, and the queue owns them.
  const [historyOpen, setHistoryOpen] = useState(false);
  const historyPageQuery = useInfiniteQuery({
    ...workspaceDecisionRequestsHistoryPageOptions(wsId ?? null),
    enabled: historyOpen,
  });
  const historyPages = historyPageQuery.data?.pages ?? [];
  const historyRows = useMemo(
    () => historyPages.flatMap((page) => page.items),
    [historyPages],
  );

  const tabs = useMemo<DecisionTab[]>(() => [...DECISION_TAB_ORDER], []);
  const tabLabel = (v: DecisionTab) => t(`mobile.tabs.${v}`, v);

  const onPressRow = (row: DecisionRowData) => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/issue/[id]",
      params: {
        workspace: wsSlug,
        id: row.issueId,
        decision: row.id,
        h: String(Date.now()),
      },
    });
  };

  const renderItem = ({
    item,
  }: SectionListRenderItemInfo<DecisionRowData, DecisionsSection>) => (
    <DecisionInboxRow row={item} onPress={() => onPressRow(item)} />
  );

  // A status TAB is one flat window — the pill already names it, so no
  // section header; 全部 keeps the RUYI-494 grouped shape.
  const showSectionHeaders = tab === "all";

  return (
    <View className="flex-1 bg-background">
      <Header title={t("mobile.page.title", "Decisions")} />
      <DecisionsToolbar
        tabs={tabs}
        tab={tab}
        tabLabel={tabLabel}
        onChange={(v) => useDecisionsViewStore.getState().setTab(v)}
      />
      {/* Keyboard-avoiding body: the search field stays above the keyboard
          and the list/load-more shrink instead of hiding under it
          (search.tsx precedent, behavior="padding" is cross-platform). */}
      <KeyboardAvoidingView className="flex-1" behavior="padding">
        <DecisionsSearchRow
          value={searchInput}
          onChange={setSearchInput}
        />
        {pageQuery.isPending && !hasWindow ? (
          <DecisionsLoading />
        ) : pageQuery.isPending && hasWindow ? (
          <View className="flex-col gap-2 px-4 py-3">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </View>
        ) : pageQuery.isError ? (
          <View className="px-4 gap-3 pt-4">
            <Text className="text-sm text-destructive">
              {t("mobile.page.load_failed", "Failed to load decision cards: {{reason}}", {
                reason:
                  pageQuery.error instanceof Error
                    ? pageQuery.error.message
                    : t("common:mobile.common.unknown_error", "unknown error"),
              })}
            </Text>
            <Button variant="outline" onPress={() => pageQuery.refetch()}>
              <Text>{t("common:mobile.common.retry", "Retry")}</Text>
            </Button>
          </View>
        ) : null}

        {!pageQuery.isPending && !pageQuery.isError && pendingRow ? (
          <DecisionsQueueCard request={pendingRow} pendingCount={requestsData?.counts.pending ?? 0} />
        ) : null}

        {!pageQuery.isPending && !pageQuery.isError && !requestsPending && historyTotal > 0 ? (
          <DecisionsHistory
            open={historyOpen}
            total={historyTotal}
            rows={historyRows}
            loading={historyOpen && historyPageQuery.data === undefined}
            hasNextPage={historyOpen && historyPageQuery.hasNextPage}
            loadingNextPage={historyOpen && historyPageQuery.isFetchingNextPage}
            onLoadMore={() => historyPageQuery.fetchNextPage()}
            onToggle={() => setHistoryOpen((open) => !open)}
          />
        ) : null}

        {pageQuery.isPending === false && !pageQuery.isError && items.length === 0 && hasWindow ? (
          <DecisionsFilteredEmpty
            onClear={() => {
              useDecisionsViewStore.getState().setTab("all");
              setSearchInput("");
            }}
          />
        ) : null}

        {pageQuery.isPending === false &&
          !pageQuery.isError &&
          !requestsPending &&
          !hasWindow &&
          items.length === 0 &&
          !pendingRow &&
          historyTotal === 0 ? (
          <DecisionsEmpty iconColor={THEME[colorScheme].mutedForeground} />
        ) : null}

        {!pageQuery.isPending && !pageQuery.isError && items.length > 0 ? (
          <SectionList
            sections={sections}
            keyExtractor={(row) => row.id}
            renderItem={renderItem}
            renderSectionHeader={
              showSectionHeaders
                ? ({ section }) => (
                    <SectionHeader
                      sectionKey={section.key}
                      count={section.count}
                    />
                  )
                : undefined
            }
            ItemSeparatorComponent={() => <View className="h-px bg-border ml-4" />}
            contentContainerClassName="pb-2"
            refreshing={pageQuery.isRefetching && !pageQuery.isFetchingNextPage}
            onRefresh={() => pageQuery.refetch()}
            stickySectionHeadersEnabled={false}
            ListFooterComponent={
              pageQuery.hasNextPage ? (
                <View className="items-center py-3">
                  <Pressable
                    accessibilityRole="button"
                    disabled={pageQuery.isFetchingNextPage}
                    onPress={() => pageQuery.fetchNextPage()}
                    className="flex-row items-center gap-2 rounded-full px-4 py-2 bg-secondary/40 active:bg-secondary"
                  >
                    {pageQuery.isFetchingNextPage ? (
                      <ActivityIndicator size="small" color={THEME[colorScheme].brand} />
                    ) : null}
                    <Text className="text-xs text-foreground">
                      {pageQuery.isFetchingNextPage
                        ? t("loading_more", "Loading…")
                        : t("load_more", "Load more")}
                    </Text>
                  </Pressable>
                </View>
              ) : null
            }
          />
        ) : null}
      </KeyboardAvoidingView>
    </View>
  );
}

/**
 * Pending authorization queue (RUYI-864) — a single card: the server's
 * oldest pending row. Renders above the card list and is the tab's sole
 * content when no cards match the window. The card's own approve/deny/cancel
 * (DecisionRequestRow) drives the queue forward; this wrapper only carries
 * the section header + pending count. The authorization summary never enters
 * the card TAB buckets (决策5) — 待决策/已决策 map the card statuses, not the
 * request lifecycle.
 */
function DecisionsQueueCard({
  request,
  pendingCount,
}: {
  request: DecisionRequest;
  pendingCount: number;
}) {
  const { t } = useT("decisions");
  const { colorScheme } = useColorScheme();
  return (
    <View>
      <View className="flex-row items-center gap-1.5 px-4 pb-1 pt-3">
        <Ionicons
          name="shield-checkmark-outline"
          size={13}
          color={THEME[colorScheme].brand}
        />
        <Text className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {t("queue.title", "Pending authorizations")}
        </Text>
        {pendingCount > 0 ? (
          <Text className="text-xs text-muted-foreground">
            {`· ${pendingCount}`}
          </Text>
        ) : null}
      </View>
      <View className="mx-4 mt-1 rounded-lg border border-border bg-secondary/30">
        <DecisionRequestRow request={request} />
      </View>
    </View>
  );
}

/**
 * Low-key collapsed authorization history (RUYI-864): a single toggle row
 * that expands to the processed requests (newest-first), coexisting with the
 * card list/filter space. Renders only when there is a processed total;
 * pending rows never appear here (the queue owns them). RUYI-864 rework:
 * the rows come from the server's terminal-state window (status=history),
 * keyset-paged — a "Load more" button rides the same button style the card
 * list uses.
 */
function DecisionsHistory({
  open,
  total,
  rows,
  loading,
  hasNextPage,
  loadingNextPage,
  onLoadMore,
  onToggle,
}: {
  open: boolean;
  total: number;
  rows: DecisionRequest[];
  loading: boolean;
  hasNextPage: boolean;
  loadingNextPage: boolean;
  onLoadMore: () => void;
  onToggle: () => void;
}) {
  const { t } = useT("decisions");
  const { colorScheme } = useColorScheme();
  return (
    <View className="mx-4 my-1 rounded-lg border border-border bg-secondary/20">
      <Pressable
        testID="decisions-history-toggle"
        accessibilityRole="button"
        accessibilityState={{ expanded: open }}
        onPress={onToggle}
        className="flex-row items-center gap-1.5 px-3 py-2 active:bg-secondary/40"
      >
        <Ionicons
          name={open ? "chevron-down-outline" : "chevron-forward-outline"}
          size={14}
          color={THEME[colorScheme].mutedForeground}
        />
        <Text className="text-xs font-medium text-muted-foreground">
          {t("history.title", "Authorization history")}
        </Text>
        <Text className="text-xs text-muted-foreground">{`· ${total}`}</Text>
      </Pressable>
      {open ? (
        <View className="border-t border-border">
          {loading ? (
            <View className="items-center justify-center py-4">
              <ActivityIndicator size="small" color={THEME[colorScheme].brand} />
            </View>
          ) : null}
          {rows.map((request) => (
            <DecisionRequestRow key={request.id} request={request} />
          ))}
          {hasNextPage ? (
            <View className="items-center py-3">
              <Pressable
                testID="decisions-history-load-more"
                accessibilityRole="button"
                disabled={loadingNextPage}
                onPress={onLoadMore}
                className="flex-row items-center gap-2 rounded-full px-4 py-2 bg-secondary/40 active:bg-secondary"
              >
                {loadingNextPage ? (
                  <ActivityIndicator size="small" color={THEME[colorScheme].brand} />
                ) : null}
                <Text className="text-xs text-foreground">
                  {loadingNextPage
                    ? t("loading_more", "Loading…")
                    : t("load_more", "Load more")}
                </Text>
              </Pressable>
            </View>
          ) : null}
        </View>
      ) : null}
    </View>
  );
}

/**
 * Search row (RUYI-864): a full-width keyword input feeding the debounced
 * `q`. Clearing it (the X) collapses back to the 全部 window.
 */
function DecisionsSearchRow({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useT("decisions");
  const { colorScheme } = useColorScheme();
  return (
    <View className="flex-row items-center gap-2 px-4 pb-2">
      <View className="flex-1 flex-row items-center gap-2 rounded-md border border-border bg-secondary/50 px-2.5 h-9">
        <Ionicons
          name="search-outline"
          size={15}
          color={THEME[colorScheme].mutedForeground}
        />
        <TextField
          value={value}
          onChangeText={onChange}
          placeholder={t("search.placeholder", "Search issue number, title, or question")}
          placeholderTextColor={THEME[colorScheme].mutedForeground}
          autoCorrect={false}
          autoCapitalize="none"
          returnKeyType="search"
          className="flex-1 h-9 border-0 bg-transparent px-0 text-sm"
        />
        {value ? (
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={t("search.clear_search", "Clear search")}
            onPress={() => onChange("")}
            className="px-1.5"
          >
            <Ionicons
              name="close-circle"
              size={16}
              color={THEME[colorScheme].mutedForeground}
            />
          </Pressable>
        ) : null}
      </View>
    </View>
  );
}

/**
 * Toolbar mirroring the Tasks tab's: horizontally scrolling TAB pills
 * (adaptive wrap above fontScale 1 — same dead-drag reasoning as
 * tasks.tsx). RUYI-864: the filter icon button is gone — the status window
 * IS the pills, and the keyword is the search row below.
 */
function DecisionsToolbar({
  tabs,
  tab,
  tabLabel,
  onChange,
}: {
  tabs: DecisionTab[];
  tab: DecisionTab;
  tabLabel: (v: DecisionTab) => string;
  onChange: (value: DecisionTab) => void;
}) {
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
    <View className="px-4 pt-2 pb-2">
      {wrapPills ? (
        <View className="flex-row flex-wrap items-center gap-1">{pills}</View>
      ) : (
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          contentContainerClassName="flex-row items-center gap-1"
        >
          {pills}
        </ScrollView>
      )}
    </View>
  );
}

function SectionHeader({
  sectionKey,
  count,
}: {
  sectionKey: DecisionRowData["status"];
  count: number;
}) {
  const { t } = useT("decisions");
  const label = t(`section.${sectionKey}`, String(sectionKey));
  return (
    <View className="px-4 pb-1 pt-4">
      <Text className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {`${label} · ${count}`}
      </Text>
    </View>
  );
}

// Loading state — row-shaped Skeletons matching the two-line row layout
// (same perceived-perf reasoning as the inbox list).
function DecisionsLoading() {
  return (
    <View className="px-4 pt-4 gap-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <View key={i} className="gap-2">
          <Skeleton className="h-3.5 w-3/4" />
          <Skeleton className="h-3 w-1/2" />
        </View>
      ))}
    </View>
  );
}

function DecisionsFilteredEmpty({ onClear }: { onClear: () => void }) {
  const { t } = useT("decisions");
  const { colorScheme } = useColorScheme();
  return (
    <View className="flex-1 items-center justify-center px-8 gap-3">
      <Ionicons
        name="funnel-outline"
        size={36}
        color={THEME[colorScheme].mutedForeground}
      />
      <Text className="text-sm text-muted-foreground text-center">
        {t("search.no_results", "No decision cards match your search or filters")}
      </Text>
      <Button variant="outline" size="sm" onPress={onClear}>
        <Text>{t("clear_window", "Clear filters and search")}</Text>
      </Button>
    </View>
  );
}

function DecisionsEmpty({ iconColor }: { iconColor: string }) {
  const { t } = useT("decisions");
  return (
    <View className="flex-1 items-center justify-center px-8 gap-3">
      <Ionicons
        name="checkmark-done-circle-outline"
        size={42}
        color={iconColor}
      />
      <Text className="text-base font-medium text-foreground text-center">
        {t("page.empty_title", "No decision cards yet")}
      </Text>
      <Text className="text-sm text-muted-foreground text-center">
        {t(
          "page.empty_description",
          "Decision cards raised by agents or members will be aggregated here, so nothing waiting on you slips through.",
        )}
      </Text>
    </View>
  );
}
