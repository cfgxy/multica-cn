/**
 * Comment timeline row. Rounded gray bubble containing the parent comment
 * plus, when applicable, every descendant reply stacked inline. The bubble
 * boundary itself is the thread indicator — no "↪ Replying to" header, no
 * recursive indentation. This matches the user's design call: "放在一个 card
 * 内部就行了 / no need for the Replying to label".
 *
 * Mobile flat-list rule (apps/mobile/CLAUDE.md): same comments as web,
 * different layout — web shows recursive tree, mobile shows one bubble per
 * thread. Counts agree (no comment is dropped or duplicated).
 *
 * Long-press on any CommentBody (parent or reply) opens
 * CommentActionSheet — the iOS-native entry point for quick reactions,
 * reply, and copy. Reactions render under each comment body via
 * ReactionBar (existing behavior, only visible when a reaction exists).
 */
import { useCallback, useState } from "react";
import { Pressable, View } from "react-native";
import * as Haptics from "expo-haptics";
import * as Clipboard from "expo-clipboard";
import type { Reaction, TimelineEntry } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { useActorLookup } from "@/data/use-actor-name";
import { timeAgo } from "@/lib/time-ago";
import { Markdown } from "@/lib/markdown";
import { useToggleCommentReaction } from "@/data/mutations/issues";
import { useAuthStore } from "@/data/auth-store";
import { ReactionBar } from "./reaction-bar";
import { CommentActionSheet } from "./comment-action-sheet";

interface Props {
  entry: TimelineEntry;
  /** Flattened descendant replies. Rendered inline below the parent inside
   *  the same bubble, separated by a hairline divider. */
  replies?: TimelineEntry[];
  /** Plumbed through so each CommentBody can wire its reaction toggle to
   *  the correct issue's mutation key. */
  issueId: string;
  /** Bubble-up callback — long-press → Reply opens this with the target
   *  comment id and display name; the issue page lifts replyingTo state. */
  onReplyTo: (commentId: string, name: string) => void;
}

export function CommentCard({ entry, replies = [], issueId, onReplyTo }: Props) {
  return (
    <View className="px-4">
      <View className="bg-secondary rounded-2xl px-4 py-3 gap-3">
        <CommentBody entry={entry} issueId={issueId} onReplyTo={onReplyTo} />
        {replies.map((reply) => (
          <View
            key={reply.id}
            className="border-t border-border/60 pt-3"
          >
            <CommentBody
              entry={reply}
              issueId={issueId}
              onReplyTo={onReplyTo}
            />
          </View>
        ))}
      </View>
    </View>
  );
}

function CommentBody({
  entry,
  issueId,
  onReplyTo,
}: {
  entry: TimelineEntry;
  issueId: string;
  onReplyTo: (commentId: string, name: string) => void;
}) {
  const { getName } = useActorLookup();
  const userId = useAuthStore((s) => s.user?.id);
  const toggle = useToggleCommentReaction(issueId);
  const [sheetOpen, setSheetOpen] = useState(false);

  const name = getName(
    entry.actor_type as "member" | "agent" | null | undefined,
    entry.actor_id,
  );
  const edited =
    entry.updated_at &&
    entry.created_at &&
    entry.updated_at !== entry.created_at;

  // Reactions live on TimelineEntry.reactions (mirrored from Comment).
  // Pass through to the bar; toggle finds existing match by emoji + actor.
  const reactions: Reaction[] = (entry.reactions ?? []) as Reaction[];

  const onToggleReaction = useCallback(
    (emoji: string) => {
      const existing = reactions.find(
        (r) =>
          r.emoji === emoji &&
          r.actor_type === "member" &&
          r.actor_id === userId,
      );
      toggle.mutate({ commentId: entry.id, emoji, existing });
    },
    [reactions, userId, toggle, entry.id],
  );

  const handleLongPress = useCallback(() => {
    // Optimistic comments (synthetic ids from the create mutation) shouldn't
    // accept actions — server-side ids haven't been assigned yet, so a
    // toggle/copy/reply against the synthetic id would no-op or break.
    if (entry.id.startsWith("optimistic-")) return;
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    setSheetOpen(true);
  }, [entry.id]);

  const handleCopy = useCallback(async () => {
    if (entry.content) {
      await Clipboard.setStringAsync(entry.content);
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    }
  }, [entry.content]);

  // Note: entry.attachments is not rendered separately — the markdown
  // renderer handles inline images (`![]()`) and file cards
  // (`!file[name](url)` → preprocessed into a 📎-prefixed link). The
  // attachments[] array is backend cleanup metadata, not display content
  // (matches web's behavior).
  return (
    <Pressable onLongPress={handleLongPress} delayLongPress={400}>
      <View className="gap-2">
        <View className="flex-row items-center gap-2">
          <ActorAvatar
            type={entry.actor_type as "member" | "agent"}
            id={entry.actor_id}
            size={24}
          />
          <Text className="text-sm font-medium text-foreground">{name}</Text>
          <Text className="text-xs text-muted-foreground">
            · {timeAgo(entry.created_at)}
            {edited ? " · (edited)" : ""}
          </Text>
        </View>
        {entry.content ? <Markdown content={entry.content} /> : null}
        <ReactionBar
          reactions={reactions}
          currentUserId={userId}
          onToggle={onToggleReaction}
        />
      </View>
      <CommentActionSheet
        visible={sheetOpen}
        onClose={() => setSheetOpen(false)}
        onReact={onToggleReaction}
        onReply={() => onReplyTo(entry.id, name)}
        onCopy={handleCopy}
      />
    </Pressable>
  );
}
