import { ActivityIndicator, FlatList, Pressable, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useQuery } from "@tanstack/react-query";
import type { Issue } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { ScreenHeader } from "@/components/ui/screen-header";
import { PriorityIcon } from "@/components/ui/priority-icon";
import { StatusIcon } from "@/components/ui/status-icon";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { myIssuesAssignedOptions } from "@/data/queries/my-issues";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";

export default function MyIssues() {
  const userId = useAuthStore((s) => s.user?.id ?? null);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { data, isLoading, error, refetch, isRefetching } = useQuery(
    myIssuesAssignedOptions(wsId, userId),
  );

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["top"]}>
      <ScreenHeader title="My Issues" subtitle={wsSlug ?? undefined} />
      {isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : error ? (
        <View className="px-4 gap-3">
          <Text className="text-sm text-destructive">
            Failed to load issues:{" "}
            {error instanceof Error ? error.message : "unknown error"}
          </Text>
          <Button variant="outline" onPress={() => refetch()}>
            Retry
          </Button>
        </View>
      ) : !data || data.length === 0 ? (
        <View className="flex-1 items-center justify-center px-6">
          <Text className="text-sm text-muted-foreground">
            No issues assigned to you.
          </Text>
        </View>
      ) : (
        <FlatList
          data={data}
          keyExtractor={(item) => item.id}
          ItemSeparatorComponent={() => (
            <View className="h-px bg-border ml-4" />
          )}
          contentContainerClassName="pb-6"
          renderItem={({ item }) => <IssueRow issue={item} />}
          refreshing={isRefetching}
          onRefresh={refetch}
        />
      )}
    </SafeAreaView>
  );
}

function IssueRow({ issue }: { issue: Issue }) {
  return (
    <Pressable className="active:bg-secondary px-4 py-3">
      <View className="flex-row items-center gap-3">
        <PriorityIcon priority={issue.priority} />
        <StatusIcon status={issue.status} />
        <Text className="text-xs text-muted-foreground shrink-0 w-16">
          {issue.identifier}
        </Text>
        <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
          {issue.title}
        </Text>
        {issue.assignee_type && issue.assignee_id ? (
          <ActorAvatar
            type={issue.assignee_type}
            id={issue.assignee_id}
            size={20}
          />
        ) : null}
      </View>
    </Pressable>
  );
}
