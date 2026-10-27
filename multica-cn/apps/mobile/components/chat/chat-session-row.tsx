/**
 * Chat session list row — the visual half of both chat lists (the tab root
 * and the Archived sub-view, RUYI-533), no gesture or navigation logic.
 *
 * Visual structure mirrors the inbox row (components/inbox/inbox-row.tsx)
 * so the two tabs read as one app — RUYI-533's style alignment ask:
 *   - avatar 36 + presence on the left; two stacked lines on the right
 *   - top line: [unread dot + title] | [pin glyph]
 *   - bottom line: preview | time-ago
 * Row hairlines and list padding are owned by the parent FlatList, like
 * the inbox list.
 */
import { Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import type { ChatSession } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { chatSessionDisplayTitle } from "@/lib/chat-session-title";
import { chatSessionPreview } from "@/lib/chat-session-preview";
import { timeAgo } from "@/lib/time-ago";
import { useT } from "@/lib/use-t";
import { cn } from "@/lib/utils";

interface Props {
  session: ChatSession;
  /** Shared "Untitled chat" fallback, resolved once by the owning screen. */
  untitled: string;
  onPress: () => void;
  onLongPress?: () => void;
}

export function ChatSessionRow({ session, untitled, onPress, onLongPress }: Props) {
  const { t } = useT("chat");
  const unread = session.has_unread;
  const preview = chatSessionPreview(session, t);
  const activityAt = session.last_message?.created_at ?? session.updated_at;

  return (
    <Pressable
      testID={`chat-row-${session.id}`}
      onPress={onPress}
      onLongPress={onLongPress}
      className="bg-background active:bg-secondary px-4 py-3"
    >
      <View className="flex-row gap-3">
        <ActorAvatar type="agent" id={session.agent_id} size={36} showPresence />
        <View className="flex-1 min-w-0">
          <View className="flex-row items-center gap-2">
            <View className="flex-row items-center gap-1.5 flex-1 min-w-0">
              {unread ? (
                <View className="size-1.5 rounded-full bg-brand shrink-0" />
              ) : null}
              <Text
                className={cn(
                  "flex-1 text-sm",
                  unread
                    ? "font-medium text-foreground"
                    : "text-muted-foreground",
                )}
                numberOfLines={1}
              >
                {chatSessionDisplayTitle(session.title, untitled)}
              </Text>
            </View>
            {session.pinned ? (
              <Ionicons
                name="pin"
                size={14}
                className="text-muted-foreground shrink-0"
              />
            ) : null}
          </View>
          <View className="flex-row items-center gap-2 mt-0.5">
            <Text
              className={cn(
                "flex-1 text-xs",
                preview.kind === "failed"
                  ? "text-destructive"
                  : unread
                    ? "text-muted-foreground"
                    : "text-muted-foreground/60",
                preview.kind === "no_response" && "italic",
              )}
              numberOfLines={1}
            >
              {preview.text}
            </Text>
            <Text
              className={cn(
                "text-xs shrink-0 tabular-nums",
                unread ? "text-muted-foreground" : "text-muted-foreground/60",
              )}
            >
              {timeAgo(activityAt)}
            </Text>
          </View>
        </View>
      </View>
    </Pressable>
  );
}
