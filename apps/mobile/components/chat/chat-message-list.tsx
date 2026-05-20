/**
 * Chat message list — user / assistant bubbles, oldest at top, newest at
 * bottom. Initial render lands at the bottom; new arrivals auto-scroll
 * when the user is anchored near the bottom; reading history is never
 * yanked down.
 *
 * Behavioral parity (apps/mobile/CLAUDE.md):
 *   - Render ALL message roles. Unknown role values are downgraded to
 *     "assistant" by ChatMessageSchema's `.catch()`, so this list never
 *     needs to silently drop a row.
 *   - Render `failure_reason` messages with destructive styling — same
 *     boolean as web's destructive bubble + failureReasonLabel().
 *
 * v1 simplifications:
 *   - No "Replied in Ns" badge under assistant bubbles (elapsed_ms is
 *     parsed but not displayed). Easy v2 add — show below the bubble.
 *   - No attachment card rendering. Attachments embedded as
 *     `![](url)` / `[name](url)` in `content` flow through the existing
 *     markdown renderer.
 *
 * List engine: FlashList v2 (Shopify). FlatList was the original choice
 * (per the now-outdated "no FlashList" baseline in apps/mobile/CLAUDE.md
 * — written before FlashList v2 stabilised). FlatList's `scrollToEnd` is
 * janky on variable-height lists by RN's own docs admission, and our
 * markdown bubbles render in multiple async passes (Shiki highlight,
 * image natural-size, lightbox provider injection) — each pass used to
 * fire onContentSizeChange and trigger another forced scroll, causing
 * the "open chat → feels stuck" jank. FlashList v2 replaces the manual
 * scroll dance with `maintainVisibleContentPosition`
 * (default-on; locks visible item across content changes) +
 * `startRenderingFromBottom` (initial paint at bottom, no setTimeout
 * hacks). Cell recycling also keeps scroll-up smooth.
 */
import { ActivityIndicator, View } from "react-native";
import { FlashList } from "@shopify/flash-list";
import type { ChatMessage } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Markdown } from "@/lib/markdown";
import { failureReasonLabel } from "@/lib/failure-reason-label";

interface Props {
  messages: ChatMessage[];
  loading: boolean;
}

export function ChatMessageList({ messages, loading }: Props) {
  if (loading && messages.length === 0) {
    return (
      <View className="flex-1 items-center justify-center">
        <ActivityIndicator />
      </View>
    );
  }

  if (messages.length === 0) {
    // Empty new-chat state. Lives here (rather than the parent screen) so
    // the empty state and the rendered list share spacing/layout rules.
    return (
      <View className="flex-1 items-center justify-center px-6">
        <Text className="text-sm text-muted-foreground text-center">
          Start the conversation.
        </Text>
      </View>
    );
  }

  return (
    // `key` on first message id forces remount on session switch so
    // `startRenderingFromBottom` re-fires and we land at the new
    // session's bottom (instead of inheriting the previous session's
    // scroll position). Cheap because sessions are switched, not
    // re-rendered every keystroke.
    <FlashList
      key={messages[0]?.id ?? "empty"}
      data={messages}
      keyExtractor={(m) => m.id}
      renderItem={({ item }) => <MessageRow message={item} />}
      ItemSeparatorComponent={MessageSeparator}
      // Outer padding mirrors web's max-w-4xl px-5 py-4 container at
      // mobile scale. Vertical gap between bubbles handled by
      // ItemSeparatorComponent (FlashList doesn't honour `gap-*` on
      // contentContainer the way FlatList's gap-via-NativeWind did).
      contentContainerStyle={{
        paddingHorizontal: 16,
        paddingTop: 12,
        paddingBottom: 16,
      }}
      // Chat behavior: initial render at the bottom; when new messages
      // arrive AND the user is within 20% of the bottom, auto-scroll.
      // Reading history (further than 20% up) is preserved. This single
      // prop replaces the entire FlatList-era guard ref dance.
      maintainVisibleContentPosition={{
        autoscrollToBottomThreshold: 0.2,
        startRenderingFromBottom: true,
      }}
      // iMessage-style keyboard dismissal: dragging the list pulls the
      // keyboard down with the finger (iOS); tapping empty space between
      // bubbles dismisses it. `handled` keeps Pressables inside bubbles
      // (long-press action sheet etc.) firing normally.
      keyboardDismissMode="interactive"
      keyboardShouldPersistTaps="handled"
    />
  );
}

function MessageSeparator() {
  return <View style={{ height: 12 }} />;
}

function MessageRow({ message }: { message: ChatMessage }) {
  const isUser = message.role === "user";
  const isFailure = !!message.failure_reason;

  if (isFailure) {
    return (
      <View className="self-start max-w-[80%] rounded-2xl border border-destructive/30 bg-destructive/10 px-3.5 py-2">
        <Text className="text-xs font-semibold text-destructive">
          {failureReasonLabel(message.failure_reason)}
        </Text>
        {message.content ? (
          <Text className="text-sm text-foreground mt-1" selectable>
            {message.content}
          </Text>
        ) : null}
      </View>
    );
  }

  if (isUser) {
    // User bubble: same Markdown pipeline as assistant — `@mention`
    // serialisation `[MUL-1](mention://issue/<id>)`, inline links, and
    // inline code resolve identically to web's
    // `packages/views/chat/components/chat-message-list.tsx` user branch.
    // Width is capped at 80% so the bubble keeps the iMessage-style
    // trailing alignment instead of stretching across the column.
    return (
      <View className="self-end max-w-[80%] rounded-2xl bg-muted px-3.5 py-2">
        <Markdown content={message.content} attachments={message.attachments} />
      </View>
    );
  }

  // Assistant: full-width inside the FlashList's px-4 content container —
  // matches web's `<div className="text-sm leading-relaxed prose prose-sm
  // max-w-none">` which has no width cap of its own and gets its left/
  // right gutter from the outer max-w-4xl px-5 container.
  return (
    <View className="w-full">
      {/* message.attachments scopes mc://file/<id> resolution to this
          message's own uploads (chat doesn't have an issue-wide list). */}
      <Markdown content={message.content} attachments={message.attachments} />
    </View>
  );
}
