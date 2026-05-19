import { useMemo } from "react";
import {
  ActionSheetIOS,
  ActivityIndicator,
  Alert,
  FlatList,
  Pressable,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import type { InboxItem } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { ScreenHeader } from "@/components/ui/screen-header";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { SwipeableInboxRow } from "@/components/inbox/swipeable-inbox-row";
import { inboxListOptions } from "@/data/queries/inbox";
import {
  useArchiveAllInbox,
  useArchiveAllReadInbox,
  useArchiveCompletedInbox,
  useArchiveInbox,
  useMarkInboxRead,
} from "@/data/mutations/inbox";
import { useWorkspaceStore } from "@/data/workspace-store";
import { deduplicateInboxItems } from "@/lib/inbox-display";

export default function Inbox() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const qc = useQueryClient();
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
  const archive = useArchiveInbox();
  const archiveAll = useArchiveAllInbox();
  const archiveAllRead = useArchiveAllReadInbox();
  const archiveCompleted = useArchiveCompletedInbox();

  const onPressItem = (item: InboxItem) => {
    if (!item.read) {
      // Synchronous optimistic write so the row visibly transitions to the
      // read style BEFORE router.push captures the source view screenshot
      // for the native stack transition. The mutation's own onMutate writes
      // optimistically too, but it awaits cancelQueries first — that one
      // microtask is enough for iOS to freeze the row in its unread state
      // inside the transition snapshot. Mark-read mutation still runs to
      // sync with the server and to fire onSettled invalidate.
      qc.setQueryData<InboxItem[]>(["inbox", wsId], (old) =>
        old?.map((i) => (i.id === item.id ? { ...i, read: true } : i)),
      );
      markRead.mutate(item.id);
    }
    if (item.issue_id && wsSlug) {
      // `highlight`: the target comment id (only present on new_comment /
      // mentioned / reaction_added notifications — backend populates
      // details.comment_id there). When undefined, expo-router strips the
      // key cleanly (no "undefined" string).
      //
      // `h`: nonce forcing the param tuple to differ each tap, so re-tapping
      // the same inbox row from a back-navigation re-fires the highlight
      // effect on the issue screen (otherwise React sees identical params
      // and skips the re-render).
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

  // Trailing batch menu — mirrors desktop's dropdown
  // (packages/views/inbox/components/inbox-page.tsx:220-235). "Archive all"
  // is destructive so it gets the iOS red treatment + Alert confirm; the
  // narrower variants fire directly because they're already filtered.
  const onPressMenu = () => {
    const options = [
      "Cancel",
      "Archive all read",
      "Archive completed",
      "Archive all",
    ];
    ActionSheetIOS.showActionSheetWithOptions(
      {
        options,
        cancelButtonIndex: 0,
        destructiveButtonIndex: 3,
        title: "Inbox",
      },
      (i) => {
        if (i === 1) archiveAllRead.mutate();
        else if (i === 2) archiveCompleted.mutate();
        else if (i === 3) {
          Alert.alert(
            "Archive all?",
            "This archives every inbox item, read or unread. You can still find them via the issue pages.",
            [
              { text: "Cancel", style: "cancel" },
              {
                text: "Archive all",
                style: "destructive",
                onPress: () => archiveAll.mutate(),
              },
            ],
          );
        }
      },
    );
  };

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader
        title="Inbox"
        right={
          <>
            <InboxMenuButton onPress={onPressMenu} />
            <HeaderActions />
          </>
        }
      />
      {isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : error ? (
        <View className="px-4 gap-3">
          <Text className="text-sm text-destructive">
            Failed to load inbox:{" "}
            {error instanceof Error ? error.message : "unknown error"}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            <Text>Retry</Text>
          </Button>
        </View>
      ) : !data || data.length === 0 ? (
        <View className="flex-1 items-center justify-center px-6">
          <Text className="text-sm text-muted-foreground">
            No inbox items.
          </Text>
        </View>
      ) : (
        <FlatList
          data={data}
          keyExtractor={(item) => item.id}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-[60px]" />
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
    </SafeAreaView>
  );
}

const HIT_SLOP = { top: 8, right: 8, bottom: 8, left: 8 } as const;

function InboxMenuButton({ onPress }: { onPress: () => void }) {
  return (
    <Pressable
      onPress={onPress}
      hitSlop={HIT_SLOP}
      className="size-9 items-center justify-center rounded-full active:bg-secondary"
      accessibilityLabel="Inbox actions"
    >
      <Ionicons name="ellipsis-horizontal" size={20} color="#3f3f46" />
    </Pressable>
  );
}
