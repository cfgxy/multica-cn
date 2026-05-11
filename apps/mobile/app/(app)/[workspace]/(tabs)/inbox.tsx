import { useMemo } from "react";
import { ActivityIndicator, FlatList, Pressable, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import type { InboxItem } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { ScreenHeader } from "@/components/ui/screen-header";
import { HeaderActions } from "@/components/ui/app-header-actions";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { StatusIcon } from "@/components/ui/status-icon";
import { InboxDetailLabel } from "@/components/inbox/detail-label";
import { inboxListOptions } from "@/data/queries/inbox";
import { useMarkInboxRead } from "@/data/mutations/inbox";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  deduplicateInboxItems,
  getInboxDisplayTitle,
} from "@/lib/inbox-display";
import { timeAgo } from "@/lib/time-ago";
import { cn } from "@/lib/utils";

export default function Inbox() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
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

  const onPressItem = (item: InboxItem) => {
    if (!item.read) markRead.mutate(item.id);
    if (item.issue_id && wsSlug) {
      router.push(`/${wsSlug}/issue/${item.issue_id}`);
    }
  };

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader title="Inbox" right={<HeaderActions />} />
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
            Retry
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
            <InboxRow item={item} onPress={() => onPressItem(item)} />
          )}
          refreshing={isRefetching}
          onRefresh={refetch}
        />
      )}
    </SafeAreaView>
  );
}

function InboxRow({
  item,
  onPress,
}: {
  item: InboxItem;
  onPress: () => void;
}) {
  const isUnread = !item.read;
  const displayTitle = getInboxDisplayTitle(item);
  const actorType = item.actor_type ?? item.recipient_type;
  const actorId = item.actor_id ?? item.recipient_id;

  return (
    <Pressable onPress={onPress} className="active:bg-secondary px-4 py-3">
      <View className="flex-row gap-3">
        <ActorAvatar type={actorType} id={actorId} size={36} />
        <View className="flex-1 min-w-0">
          {/* Top row: [unread dot + title] (left) | [status icon] (right) */}
          <View className="flex-row items-center gap-2">
            <View className="flex-row items-center gap-1.5 flex-1 min-w-0">
              {isUnread ? (
                <View className="size-1.5 rounded-full bg-brand shrink-0" />
              ) : null}
              <Text
                className={cn(
                  "flex-1 text-sm",
                  isUnread
                    ? "font-medium text-foreground"
                    : "text-muted-foreground",
                )}
                numberOfLines={1}
              >
                {displayTitle}
              </Text>
            </View>
            {item.issue_status ? (
              <StatusIcon status={item.issue_status} size={14} />
            ) : null}
          </View>
          {/* Bottom row: [type-aware detail label] (left) | [time] (right).
              Detail label mirrors web InboxDetailLabel — same per-type
              wording (Mentioned / Set status to ... / Assigned to ... / etc),
              not the raw markdown body. */}
          <View className="flex-row items-center gap-2 mt-0.5">
            <View className="flex-1 min-w-0">
              <InboxDetailLabel
                item={item}
                className={
                  isUnread
                    ? "text-muted-foreground"
                    : "text-muted-foreground/60"
                }
              />
            </View>
            <Text
              className={cn(
                "text-xs shrink-0",
                isUnread
                  ? "text-muted-foreground"
                  : "text-muted-foreground/60",
              )}
            >
              {timeAgo(item.created_at)}
            </Text>
          </View>
        </View>
      </View>
    </Pressable>
  );
}
