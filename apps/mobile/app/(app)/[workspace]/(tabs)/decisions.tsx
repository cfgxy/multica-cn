/**
 * 决策中心 (RUYI-494) — workspace-level decision card aggregation, the
 * mobile home of the bottom 「决策」 tab.
 *
 * One row per CARD, never folded by issue (Owner IA on RUYI-494): line 1 =
 * issue identifier + issue title (+ 「有推荐」 badge), line 2 = that card's
 * own question + time-ago. Status lives in the section headers, 待决策
 * first — both shaped by lib/decision-inbox-display.ts, the node-tested
 * mirror of web's packages/views/decisions/components/
 * decision-center-page.tsx grouping (same server counts, same section
 * order, same empty-section drop).
 *
 * Tap → the card's issue with `decision` + `h` params, the same re-tap
 * nonce idiom the inbox deep-link uses (`apps/mobile/app/(app)/[workspace]/
 * issue/[id].tsx`): the timeline bounded-locates the card and flashes it
 * (mirrors web's `#decision-<id>` deep link).
 */
import { useMemo } from "react";
import {
  Pressable,
  SectionList,
  View,
  type SectionListRenderItemInfo,
} from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Header } from "@/components/ui/header";
import { workspaceDecisionInboxOptions } from "@/data/queries/decisions";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { timeAgo } from "@/lib/time-ago";
import {
  groupDecisionInboxSections,
  type DecisionInboxRow,
  type DecisionInboxSectionKey,
} from "@/lib/decision-inbox-display";
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

  const sections = useMemo(
    () => (inbox ? groupDecisionInboxSections(inbox) : []),
    [inbox],
  );

  const onPressRow = (row: DecisionInboxRow) => {
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
  }: SectionListRenderItemInfo<DecisionInboxRow, DecisionInboxSectionData>) => (
    <Pressable
      accessibilityRole="button"
      className="px-4 py-3 active:bg-muted"
      onPress={() => onPressRow(item)}
    >
      {/* Line 1 — identifier is the stable anchor and must stay readable at
          large system sizes, same rule as the issue detail header title. */}
      <View className="flex-row items-center gap-2">
        <Text
          numberOfLines={1}
          maxFontSizeMultiplier={1}
          className="shrink-0 text-sm font-semibold text-foreground"
        >
          {item.identifier}
        </Text>
        <Text
          numberOfLines={1}
          className="flex-1 text-sm font-medium text-foreground"
        >
          {item.issueTitle}
        </Text>
        {item.recommended ? (
          <View className="shrink-0 rounded-full bg-brand/10 px-2 py-0.5">
            <Text className="text-xs text-brand">
              {t("badge.recommended", "Has recommendation")}
            </Text>
          </View>
        ) : null}
      </View>
      {/* Line 2 — the card's own question; two cards on one issue differ
          here, which is the whole point of the one-row-per-card IA. */}
      <View className="mt-1 flex-row items-center gap-2">
        <Text
          numberOfLines={1}
          className="flex-1 text-sm text-muted-foreground"
        >
          {item.question}
        </Text>
        <Text
          numberOfLines={1}
          maxFontSizeMultiplier={1}
          className="shrink-0 text-xs text-muted-foreground"
        >
          {timeAgo(item.createdAt)}
        </Text>
      </View>
    </Pressable>
  );

  return (
    <View className="flex-1 bg-background">
      <Header title={t("mobile.page.title", "Decisions")} />
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
        <DecisionsEmpty iconColor={THEME[colorScheme].mutedForeground} />
      ) : (
        <SectionList
          sections={sections.map((s) => ({
            key: s.key,
            count: s.count,
            data: s.rows,
          }))}
          keyExtractor={(row) => row.id}
          renderItem={renderItem}
          renderSectionHeader={({ section }) => (
            <SectionHeader
              sectionKey={section.key as DecisionInboxSectionKey}
              count={section.count}
            />
          )}
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
  key: string;
  count: number;
  data: DecisionInboxRow[];
};

function SectionHeader({
  sectionKey,
  count,
}: {
  sectionKey: DecisionInboxSectionKey;
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
