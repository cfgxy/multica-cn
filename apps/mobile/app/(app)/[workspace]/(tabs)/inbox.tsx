import { ActivityIndicator, FlatList, Pressable, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useQuery } from "@tanstack/react-query";
import type { InboxItem } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { ScreenHeader } from "@/components/ui/screen-header";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { StatusIcon } from "@/components/ui/status-icon";
import { inboxListOptions } from "@/data/queries/inbox";
import { useWorkspaceStore } from "@/data/workspace-store";
import { getInboxDisplayTitle } from "@/lib/inbox-display";
import { timeAgo } from "@/lib/time-ago";
import { cn } from "@/lib/utils";

export default function Inbox() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { data, isLoading, error, refetch, isRefetching } = useQuery(
    inboxListOptions(wsId),
  );

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader title="Inbox" subtitle={wsSlug ?? undefined} />
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
          renderItem={({ item }) => <InboxRow item={item} />}
          refreshing={isRefetching}
          onRefresh={refetch}
        />
      )}
    </SafeAreaView>
  );
}

function InboxRow({ item }: { item: InboxItem }) {
  const isUnread = !item.read;
  const displayTitle = getInboxDisplayTitle(item);
  const actorType = item.actor_type ?? item.recipient_type;
  const actorId = item.actor_id ?? item.recipient_id;

  return (
    <Pressable className="active:bg-secondary px-4 py-3">
      <View className="flex-row gap-3">
        <ActorAvatar type={actorType} id={actorId} size={36} />
        <View className="flex-1 min-w-0">
          {/* Top row: unread dot + title + status icon + time */}
          <View className="flex-row items-center gap-1.5">
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
            {item.issue_status ? (
              <StatusIcon status={item.issue_status} />
            ) : null}
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
          {/* Bottom row: body */}
          {item.body ? (
            <Text
              className={cn(
                "text-xs mt-0.5",
                isUnread
                  ? "text-muted-foreground"
                  : "text-muted-foreground/60",
              )}
              numberOfLines={1}
            >
              {item.body}
            </Text>
          ) : null}
        </View>
      </View>
    </Pressable>
  );
}
