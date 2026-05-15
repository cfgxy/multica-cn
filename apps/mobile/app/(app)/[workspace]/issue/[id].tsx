/**
 * Issue detail screen (V1).
 *
 * Read-mostly + comment composer. Property edits, replies, reactions,
 * attachments inline render, mention chips, and image lightbox are deferred
 * to V2+ — see /Users/qingnaiyuan/.claude/plans/plan-dynamic-narwhal.md.
 *
 * Header note: the parent _layout.tsx already declares the
 * `issue/[id]` Stack.Screen with title "Issue". We override that here once
 * the data lands so the navigation bar shows `MUL-123` (Linear-style).
 */
import { useCallback, useEffect, useState } from "react";
import {
  ActionSheetIOS,
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Linking,
  Platform,
  Pressable,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, router, useLocalSearchParams } from "expo-router";
import { useHeaderHeight } from "@react-navigation/elements";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Ionicons } from "@expo/vector-icons";
import * as Clipboard from "expo-clipboard";
import type { Issue } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { TimelineList } from "@/components/issue/timeline-list";
import { CommentComposer } from "@/components/issue/comment-composer";
import {
  issueDetailOptions,
  issueKeys,
  issueTimelineOptions,
} from "@/data/queries/issues";
import { useCreateComment, useDeleteIssue } from "@/data/mutations/issues";
import { useIssueRealtime } from "@/data/realtime/use-issue-realtime";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useViewedIssuesStore } from "@/data/viewed-issues-store";

export default function IssueDetail() {
  // `highlight` + `h` come from inbox deep-link (apps/mobile/app/(app)/
  // [workspace]/(tabs)/inbox.tsx). `highlight` is the target comment id;
  // `h` is a per-tap nonce so re-tapping the same row re-fires the
  // scroll-and-flash effect.
  const { id, highlight, h } = useLocalSearchParams<{
    id: string;
    highlight?: string;
    h?: string;
  }>();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const qc = useQueryClient();
  // KeyboardAvoidingView's `padding` behaviour calculates from screen top.
  // The native iOS Stack header above this screen takes ~88pt that the
  // padding doesn't subtract — without this offset, the comment composer
  // ends up under the keyboard by exactly the header height. See
  // https://reactnavigation.org/docs/use-header-height.
  const headerHeight = useHeaderHeight();

  const detail = useQuery(issueDetailOptions(wsId, id));
  const timeline = useQuery(issueTimelineOptions(wsId, id));
  const createComment = useCreateComment(id);

  // Subscribe to per-issue WS events: status/priority/assignee/label
  // changes, comments, activity, reactions, agent task progress.
  // Mounted with `id` — cleans up automatically on navigate-away.
  // If another client deletes the issue we're viewing, pop back so the
  // user isn't stranded on a 404 detail page.
  useIssueRealtime(id, () => router.back());

  // Track viewed issues so the chat composer's `@` suggestion bar can
  // surface "Recent" — the user just looked at MUL-123, likely wants to
  // ask the agent about it next. Workspace-scoped + in-memory; see
  // data/viewed-issues-store.ts.
  useEffect(() => {
    if (wsId && id) {
      useViewedIssuesStore.getState().push(wsId, id);
    }
  }, [wsId, id]);

  // Lifted: long-press a comment → action sheet → "Reply" sets this; the
  // composer reads it to render a "Replying to <name>" chip and sends the
  // resulting comment with `parent_id`.
  const [replyingTo, setReplyingTo] = useState<{
    commentId: string;
    name: string;
  } | null>(null);

  const onRefresh = useCallback(async () => {
    await Promise.all([
      detail.refetch(),
      qc.invalidateQueries({ queryKey: issueKeys.timeline(wsId, id) }),
    ]);
  }, [detail, qc, wsId, id]);

  const onSubmitComment = useCallback(
    async (vars: { content: string; parentId?: string }) => {
      await createComment.mutateAsync(vars);
      setReplyingTo(null);
    },
    [createComment],
  );

  const onReplyTo = useCallback((commentId: string, name: string) => {
    setReplyingTo({ commentId, name });
  }, []);

  const onCancelReply = useCallback(() => setReplyingTo(null), []);

  const issue = detail.data;
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const deleteIssue = useDeleteIssue();

  // Three-dot menu: Copy link / Open on web (if web URL set) / Delete.
  // Mirrors apps/mobile/app/(app)/[workspace]/project/[id].tsx:99-148 — same
  // ActionSheetIOS + Alert.alert confirm pattern. Property edits (status,
  // priority, assignee, due_date) live on the IssueHeaderCard chips inside
  // the timeline list, not in this menu — one entry per action.
  const onPressMore = useCallback(() => {
    if (!issue || !wsSlug) return;
    const webUrl = process.env.EXPO_PUBLIC_WEB_URL;
    const issueLink = webUrl
      ? `${webUrl}/${wsSlug}/issue/${issue.identifier}`
      : null;
    const options: string[] = ["Cancel"];
    if (issueLink) options.push("Copy link");
    if (issueLink) options.push("Open on web");
    options.push("Delete issue");
    const destructiveIndex = options.length - 1;
    ActionSheetIOS.showActionSheetWithOptions(
      {
        options,
        cancelButtonIndex: 0,
        destructiveButtonIndex: destructiveIndex,
        title: issue.identifier,
      },
      (i) => {
        const label = options[i];
        if (label === "Copy link" && issueLink) {
          Clipboard.setStringAsync(issueLink);
        } else if (label === "Open on web" && issueLink) {
          Linking.openURL(issueLink);
        } else if (label === "Delete issue") {
          confirmDelete(issue, () =>
            deleteIssue.mutate(issue.id, {
              onSuccess: () => router.back(),
            }),
          );
        }
      },
    );
  }, [issue, wsSlug, deleteIssue]);

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen
        options={{
          title: issue?.identifier ?? "Issue",
          headerBackTitle: "Back",
          headerRight: issue
            ? () => (
                <Pressable
                  onPress={onPressMore}
                  hitSlop={8}
                  className="px-2 py-1"
                  accessibilityLabel="Issue actions"
                >
                  <Ionicons
                    name="ellipsis-horizontal"
                    size={20}
                    color={Platform.OS === "ios" ? "#0a84ff" : "#71717a"}
                  />
                </Pressable>
              )
            : undefined,
        }}
      />
      {detail.isLoading ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator />
        </View>
      ) : detail.error || !issue ? (
        <View className="flex-1 items-center justify-center px-6 gap-3">
          <Text className="text-sm text-destructive text-center">
            Failed to load issue:{" "}
            {detail.error instanceof Error
              ? detail.error.message
              : "not found"}
          </Text>
          <Button variant="outline" onPress={() => detail.refetch()}>
            Retry
          </Button>
        </View>
      ) : (
        <KeyboardAvoidingView
          behavior={Platform.OS === "ios" ? "padding" : undefined}
          keyboardVerticalOffset={headerHeight}
          className="flex-1"
        >
          <View className="flex-1">
            <TimelineList
              issue={issue}
              entries={timeline.data}
              timelineLoading={timeline.isLoading}
              refreshing={detail.isRefetching || timeline.isRefetching}
              onRefresh={onRefresh}
              onReplyTo={onReplyTo}
              highlightCommentId={highlight}
              highlightNonce={h}
            />
          </View>
          <CommentComposer
            issueId={id}
            onSubmit={onSubmitComment}
            replyingTo={replyingTo}
            onCancelReply={onCancelReply}
          />
        </KeyboardAvoidingView>
      )}
    </SafeAreaView>
  );
}

function confirmDelete(issue: Issue, onConfirm: () => void) {
  Alert.alert(
    "Delete issue?",
    `${issue.identifier} and its comments, reactions, and attachments will be permanently deleted. This cannot be undone.`,
    [
      { text: "Cancel", style: "cancel" },
      { text: "Delete", style: "destructive", onPress: onConfirm },
    ],
  );
}
