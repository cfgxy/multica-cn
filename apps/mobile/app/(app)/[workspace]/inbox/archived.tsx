/**
 * Archived inbox sub-view (RUYI-532) — the mobile counterpart of web's
 * `?view=archived` (packages/views/inbox/components/inbox-page.tsx). Reached
 * from the "Archived" entry at the bottom of the main inbox list; the header
 * (title + back) comes from the Stack.Screen registration in
 * [workspace]/_layout.tsx, like issue/[id].
 *
 * Data source is the same capped archived endpoint web uses — the server
 * decides list membership, so this view renders `archivedInboxOptions`
 * deduplicated with the archived companion of the main list's dedup helper.
 * Rows keep the main list's press behavior (open marks read — web does the
 * same from the archived view), and the swipe action swaps Archive →
 * Unarchive; the optimistic patch inside useUnarchiveInbox drops the row
 * from this list at once and the main list + entry count recover through the
 * settle invalidation.
 */
import { useMemo } from "react";
import { FlatList, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type { InboxItem } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { SwipeableInboxRow } from "@/components/inbox/swipeable-inbox-row";
import { archivedInboxOptions } from "@/data/queries/inbox";
import { useMarkInboxRead, useUnarchiveInbox } from "@/data/mutations/inbox";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import {
  deduplicateArchivedInboxItems,
  getInboxNavigationTarget,
} from "@/lib/inbox-display";
import { useT } from "@/lib/use-t";

export default function ArchivedInbox() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("inbox");
  const {
    data: rawItems,
    isLoading,
    error,
    refetch,
    isRefetching,
  } = useQuery(archivedInboxOptions(wsId));
  const data = useMemo(
    () => deduplicateArchivedInboxItems(rawItems ?? []),
    [rawItems],
  );
  const markRead = useMarkInboxRead();
  const unarchive = useUnarchiveInbox();

  const onPressItem = (item: InboxItem) => {
    if (!item.read) markRead.mutate(item.id);
    const target = getInboxNavigationTarget(item, wsSlug, String(Date.now()));
    if (target) router.push(target);
  };

  return (
    <View className="flex-1 bg-background">
      {isLoading ? (
        <ArchivedLoading />
      ) : error ? (
        <View className="px-4 gap-3 pt-4">
          <Text className="text-sm text-destructive">
            {t("mobile.page.load_failed", "Failed to load inbox: {{reason}}", {
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
      ) : data.length === 0 ? (
        <ArchivedEmpty iconColor={THEME[colorScheme].mutedForeground} />
      ) : (
        <FlatList
          data={data}
          keyExtractor={(item) => item.id}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-16" />
          )}
          contentContainerClassName="pb-6"
          renderItem={({ item }) => (
            <SwipeableInboxRow
              item={item}
              onPress={() => onPressItem(item)}
              onArchive={() => unarchive.mutate(item.id)}
              action="unarchive"
              showUnread={false}
            />
          )}
          refreshing={isRefetching}
          onRefresh={refetch}
        />
      )}
    </View>
  );
}

function ArchivedLoading() {
  return (
    <View className="px-4 pt-4 gap-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <View key={i} className="flex-row gap-3">
          <Skeleton className="size-9 rounded-full" />
          <View className="flex-1 gap-2 pt-1">
            <Skeleton className="h-3.5 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
          </View>
        </View>
      ))}
    </View>
  );
}

function ArchivedEmpty({ iconColor }: { iconColor: string }) {
  const { t } = useT("inbox");
  return (
    <View className="flex-1 items-center justify-center px-8 gap-3">
      <Ionicons name="archive-outline" size={42} color={iconColor} />
      <Text className="text-base font-medium text-foreground text-center">
        {t("list.archived_empty", "No archived notifications")}
      </Text>
    </View>
  );
}
