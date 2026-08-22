import { useMemo } from "react";
import {
  Alert,
  FlatList,
  View,
} from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type { InboxItem } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Header } from "@/components/ui/header";
import { IconButton } from "@/components/ui/icon-button";
import { HeaderActions } from "@/components/ui/app-header-actions";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { SwipeableInboxRow } from "@/components/inbox/swipeable-inbox-row";
import { inboxListOptions } from "@/data/queries/inbox";
import {
  useArchiveAllInbox,
  useArchiveAllReadInbox,
  useArchiveCompletedInbox,
  useArchiveInbox,
  useMarkAllInboxRead,
  useMarkInboxRead,
} from "@/data/mutations/inbox";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { deduplicateInboxItems } from "@/lib/inbox-display";
import i18n from "i18next";
import { useT } from "@/lib/use-t";

export default function Inbox() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { colorScheme } = useColorScheme();
  const { t } = useT("inbox");
  const { data: rawItems, isLoading, error, refetch, isRefetching } = useQuery(
    inboxListOptions(wsId),
  );
  // Dedup + drop archived to match web/desktop. See CLAUDE.md
  // "Behavioral parity" → inbox dedup incident.
  const data = useMemo(
    () => deduplicateInboxItems(rawItems ?? []),
    [rawItems],
  );
  const markRead = useMarkInboxRead();
  const markAllRead = useMarkAllInboxRead();
  const archive = useArchiveInbox();
  const archiveAll = useArchiveAllInbox();
  const archiveAllRead = useArchiveAllReadInbox();
  const archiveCompleted = useArchiveCompletedInbox();

  const onPressItem = (item: InboxItem) => {
    if (!item.read) {
      // Optimistic read flip lives in useMarkInboxRead.onMutate — fires
      // setQueryData synchronously before the cancelQueries await, so the
      // row is already styled "read" by the time iOS captures the source
      // snapshot for the native stack push transition.
      markRead.mutate(item.id);
    }
    if (item.issue_id && wsSlug) {
      router.push({
        pathname: "/[workspace]/issue/[id]",
        params: {
          workspace: wsSlug,
          id: item.issue_id,
          highlight: item.details?.comment_id,
          h: String(Date.now()),
        },
      });
    }
  };

  // Trailing batch menu — mirrors web's dropdown
  // (packages/views/inbox/components/inbox-page.tsx). "Mark all read" is
  // first (most common batch op); "Archive all" is destructive so it gets
  // the iOS red treatment + Alert confirm.
  const onMarkAllRead = () => markAllRead.mutate();
  const onArchiveAllRead = () => archiveAllRead.mutate();
  const onArchiveCompleted = () => archiveCompleted.mutate();
  const onArchiveAll = () => {
    Alert.alert(
      t("menu.archive_all", "Archive all"),
      "This archives every inbox item, read or unread. You can still find them via the issue pages." /* mobile-only string; no web resource key */,
      [
        // "Cancel" 是字面量：inbox ns 没有 cancel key，跨 ns 取 common:cancel 需 i18n.t
        { text: i18n.t("common:cancel", "Cancel"), style: "cancel" },
        {
          text: t("menu.archive_all", "Archive all"),
          style: "destructive",
          onPress: () => archiveAll.mutate(),
        },
      ],
    );
  };

  return (
    <View className="flex-1 bg-background">
      <Header
        title={t("page.title", "Inbox")}
        right={
          <>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <IconButton
                  name="ellipsis-horizontal"
                  accessibilityLabel="Inbox actions" /* mobile-only string; no web resource key */
                />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-48">
                <DropdownMenuItem onPress={onMarkAllRead}>
                  <Text>{t("menu.mark_all_read", "Mark all as read")}</Text>
                </DropdownMenuItem>
                <DropdownMenuItem onPress={onArchiveAllRead}>
                  <Text>{t("menu.archive_all_read", "Archive all read")}</Text>
                </DropdownMenuItem>
                <DropdownMenuItem onPress={onArchiveCompleted}>
                  <Text>{t("menu.archive_completed", "Archive completed")}</Text>
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onPress={onArchiveAll}>
                  <Text>{t("menu.archive_all", "Archive all")}</Text>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <HeaderActions />
          </>
        }
      />
      {isLoading ? (
        <InboxLoading />
      ) : error ? (
        <View className="px-4 gap-3 pt-4">
          <Text className="text-sm text-destructive">
            Failed to load inbox:{" "}
            {error instanceof Error ? error.message : "unknown error"}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            <Text>Retry</Text>
          </Button>
        </View>
      ) : !data || data.length === 0 ? (
        <InboxEmpty iconColor={THEME[colorScheme].mutedForeground} />
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
              onArchive={() => archive.mutate(item.id)}
            />
          )}
          refreshing={isRefetching}
          onRefresh={refetch}
        />
      )}
    </View>
  );
}

// Loading state — 6 row-shaped Skeletons matching InboxRow's layout
// (avatar circle + two text lines). Perceived perf wins over a centered
// spinner because the eye immediately sees the list-like structure.
function InboxLoading() {
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

function InboxEmpty({ iconColor }: { iconColor: string }) {
  const { t } = useT("inbox");
  return (
    <View className="flex-1 items-center justify-center px-8 gap-3">
      <Ionicons name="mail-open-outline" size={42} color={iconColor} />
      <Text className="text-base font-medium text-foreground text-center">
        {t("list.empty", "No notifications")}
      </Text>
      <Text className="text-sm text-muted-foreground text-center">
        {t("detail.empty", "Your inbox is empty")}
      </Text>
    </View>
  );
}
