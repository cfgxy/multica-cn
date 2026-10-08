/**
 * 决策中心 (RUYI-494/530) — workspace-level decision card aggregation, the
 * mobile home of the bottom 「决策」 tab.
 *
 * One row per CARD, never folded by issue (Owner IA on RUYI-494): line 1 =
 * issue identifier + issue title (+ 「有推荐」 badge), line 2 = that card's
 * own question + time-ago; the left column carries the card creator's
 * avatar (RUYI-530). Decided rows (answered/cancelled) render the inbox's
 * read style — muted title, faded secondary — open rows keep full contrast.
 *
 * RUYI-530 adds the Tasks-tab toolbar mechanics: status TAB pills
 * (全部/待决策/已决策/已失效 — a pill maps 1:1 to a card status, the same
 * "全部 + one pill per window" shape as Tasks) + a filter button (有推荐 /
 * Agent 发起 toggles in a formSheet) + active filter chips. TAB switches
 * keep the filters (decisions-view-store, same conventions as
 * tasks-view-store; the filters apply on every TAB). On 全部 the list
 * keeps the section-grouped shape built by lib/decision-inbox-display.ts
 * (待决策 → 已决策 → 已失效); a status TAB renders one flat window — the
 * pill label already names it, so no section header. Tap → the card's
 * issue with `decision` + `h` params, the same re-tap nonce idiom the
 * inbox deep-link uses.
 */
import { useEffect, useMemo } from "react";
import {
  Pressable,
  SectionList,
  ScrollView,
  View,
  useWindowDimensions,
  type SectionListRenderItemInfo,
} from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Header } from "@/components/ui/header";
import { DecisionInboxRow } from "@/components/decision/decision-inbox-row";
import { workspaceDecisionInboxOptions } from "@/data/queries/decisions";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useDecisionsViewStore } from "@/data/stores/decisions-view-store";
import {
  DECISION_SECTION_ORDER,
  DECISION_TAB_ORDER,
  filterDecisionRows,
  toDecisionInboxRow,
  type DecisionInboxRow as DecisionRowData,
  type DecisionTab,
} from "@/lib/decision-inbox-display";
import { shouldWrapTaskPills } from "@/lib/task-toolbar";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function Decisions() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("decisions");
  const {
    data: inbox,
    isLoading,
    error,
    refetch,
    isRefetching,
  } = useQuery(workspaceDecisionInboxOptions(wsId ?? null));

  // Workspace-scoped filters live in a module-global store while this screen
  // remounts per workspace — same syncWorkspace pattern as the Tasks tab
  // (the switch writes the new id before the remounted screen syncs).
  useEffect(() => {
    useDecisionsViewStore.getState().syncWorkspace(wsId);
  }, [wsId]);

  const tab = useDecisionsViewStore((s) => s.tab);
  const recommendedOnly = useDecisionsViewStore((s) => s.recommendedOnly);
  const agentCreatedOnly = useDecisionsViewStore((s) => s.agentCreatedOnly);
  const filtersActive = recommendedOnly || agentCreatedOnly;

  const tabs = useMemo<DecisionTab[]>(() => [...DECISION_TAB_ORDER], []);
  const tabLabel = (v: DecisionTab) => t(`mobile.tabs.${v}`, v);

  const openFilter = () => {
    if (!wsSlug) return;
    router.push({
      pathname: "/[workspace]/decisions-filter",
      params: { workspace: wsSlug },
    });
  };

  // TAB owns the status window; the sheet toggles compose as AND. Server
  // order passes through — this only drops rows, never reorders.
  const filteredRows = useMemo(
    () =>
      inbox
        ? filterDecisionRows(inbox.items.map(toDecisionInboxRow), {
            tab,
            recommendedOnly,
            agentCreatedOnly,
          })
        : [],
    [inbox, tab, recommendedOnly, agentCreatedOnly],
  );

  // 全部 keeps the RUYI-494 grouped shape; a status TAB is one flat window
  // (no header — the pill names it). Section counts stay the server's
  // workspace totals while no filter is active (RUYI-494 parity: the badge,
  // the header and web can never disagree); with a filter active the header
  // must match what's visible, so it falls back to the filtered row count.
  const sections = useMemo(() => {
    if (!inbox) return [];
    if (tab !== "all") {
      return [{ key: tab, count: filteredRows.length, data: filteredRows }];
    }
    return DECISION_SECTION_ORDER.map((key) => {
      const rows = filteredRows.filter((row) => row.status === key);
      return {
        key,
        count: filtersActive ? rows.length : inbox.counts[key],
        data: rows,
      };
    }).filter((section) => section.data.length > 0);
  }, [inbox, tab, filteredRows, filtersActive]);

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
  }: SectionListRenderItemInfo<DecisionRowData, DecisionInboxSectionData>) => (
    <DecisionInboxRow row={item} onPress={() => onPressRow(item)} />
  );

  return (
    <View className="flex-1 bg-background">
      <Header title={t("mobile.page.title", "Decisions")} />
      <DecisionsToolbar
        tabs={tabs}
        tab={tab}
        tabLabel={tabLabel}
        onChange={(v) => useDecisionsViewStore.getState().setTab(v)}
        onOpenFilter={openFilter}
        filterActive={filtersActive}
      />
      {filtersActive ? (
        <View className="flex-row flex-wrap gap-1.5 px-4 pb-2">
          {recommendedOnly ? (
            <Chip
              label={t("mobile.filters.recommended_only", "Recommended only")}
              onClear={() =>
                useDecisionsViewStore.getState().toggleRecommendedOnly()
              }
            />
          ) : null}
          {agentCreatedOnly ? (
            <Chip
              label={t("mobile.filters.agent_created_only", "Agent-created only")}
              onClear={() =>
                useDecisionsViewStore.getState().toggleAgentCreatedOnly()
              }
            />
          ) : null}
        </View>
      ) : null}
      {isLoading ? (
        <DecisionsLoading />
      ) : error ? (
        <View className="px-4 gap-3 pt-4">
          <Text className="text-sm text-destructive">
            {t("mobile.page.load_failed", "Failed to load decision cards: {{reason}}", {
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
      ) : sections.length === 0 ? (
        filtersActive || tab !== "all" ? (
          <DecisionsFilteredEmpty />
        ) : (
          <DecisionsEmpty iconColor={THEME[colorScheme].mutedForeground} />
        )
      ) : (
        <SectionList
          sections={sections}
          keyExtractor={(row) => row.id}
          renderItem={renderItem}
          renderSectionHeader={({ section }) =>
            tab === "all" ? (
              <SectionHeader sectionKey={section.key} count={section.count} />
            ) : null
          }
          ItemSeparatorComponent={() => <View className="h-px bg-border ml-4" />}
          contentContainerClassName="pb-6"
          refreshing={isRefetching}
          onRefresh={refetch}
          stickySectionHeadersEnabled={false}
        />
      )}
    </View>
  );
}

type DecisionInboxSectionData = {
  key: DecisionRowData["status"];
  count: number;
  data: DecisionRowData[];
};

/**
 * Toolbar mirroring the Tasks tab's: horizontally scrolling TAB pills
 * (adaptive wrap above fontScale 1 — same dead-drag reasoning as
 * tasks.tsx) + the filter icon button on the right (red dot when active).
 */
function DecisionsToolbar({
  tabs,
  tab,
  tabLabel,
  onChange,
  onOpenFilter,
  filterActive,
}: {
  tabs: DecisionTab[];
  tab: DecisionTab;
  tabLabel: (v: DecisionTab) => string;
  onChange: (value: DecisionTab) => void;
  onOpenFilter: () => void;
  filterActive: boolean;
}) {
  const { t } = useT("decisions");
  const { colorScheme } = useColorScheme();
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
      <View style={{ position: "relative" }} className="ml-2">
        <Button
          variant="outline"
          size="sm"
          onPress={onOpenFilter}
          accessibilityLabel={t("mobile.filters.a11y", "Filter")}
          className="w-9 px-0"
        >
          <Ionicons
            name="options-outline"
            size={16}
            color={THEME[colorScheme].mutedForeground}
          />
        </Button>
        {filterActive ? (
          <View
            pointerEvents="none"
            className="absolute top-1 right-1 size-1.5 rounded-full bg-brand"
          />
        ) : null}
      </View>
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

function DecisionsFilteredEmpty() {
  const { t } = useT("decisions");
  const { colorScheme } = useColorScheme();
  return (
    <View className="flex-1 items-center justify-center px-8 gap-2">
      <Ionicons
        name="funnel-outline"
        size={36}
        color={THEME[colorScheme].mutedForeground}
      />
      <Text className="text-sm text-muted-foreground text-center">
        {t("mobile.page.empty_filtered", "No decision cards match the current TAB or filters")}
      </Text>
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
