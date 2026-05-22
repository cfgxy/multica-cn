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
 * Interaction: long-press inside a bubble fires a native iOS
 * `ActionSheetIOS` (Copy / Select Text / Cancel). While the sheet is on
 * screen the targeted bubble's border highlights. The assistant branch
 * has no border baseline because its bubble has no shell — adding a 2px
 * baseline would shift layout per message. See `useChatMessageLongPress`
 * in `./message-long-press.tsx`.
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
import { ActivityIndicator, Pressable, View } from "react-native";
import { FlashList } from "@shopify/flash-list";
import type { ChatMessage } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Markdown } from "@/lib/markdown";
import { failureReasonLabel } from "@/lib/failure-reason-label";
import { cn } from "@/lib/utils";
import { useChatSelectStore } from "@/data/chat-select-store";
import { useChatMessageLongPress } from "./message-long-press";

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
      // Any user-initiated scroll exits message text-selection mode —
      // matches iMessage's behavior where scrolling implicitly commits /
      // dismisses the selection caret. Hooks both drag-start and the
      // momentum kick after a flick so a fast scroll can't escape.
      onScrollBeginDrag={() => useChatSelectStore.getState().clear()}
      onMomentumScrollBegin={() => useChatSelectStore.getState().clear()}
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
  const isSelecting = useChatSelectStore(
    (s) => s.selectingId === message.id,
  );
  const longPress = useChatMessageLongPress(message);

  if (isFailure) {
    // B6: pass `selectable={isSelecting}` rather than hard-coding
    // `selectable` — otherwise UIKit's text-selection gesture pre-empts
    // our long-press handler and the action sheet never fires.
    // Select-mode cue is the border-tint to primary; bg stays destructive
    // so the failure signal is never lost.
    const body = (
      <View
        className={cn(
          "self-start max-w-[80%] rounded-2xl border-2 bg-destructive/10 px-3.5 py-2 transition-colors",
          isSelecting || longPress.isPressed
            ? "border-primary/30"
            : "border-destructive/30",
        )}
      >
        <Text className="text-xs font-semibold text-destructive">
          {failureReasonLabel(message.failure_reason)}
        </Text>
        {message.content ? (
          <Text
            className="text-sm text-foreground mt-1"
            selectable={isSelecting}
          >
            {message.content}
          </Text>
        ) : null}
      </View>
    );
    if (isSelecting) return body;
    return (
      <Pressable
        onLongPress={longPress.onLongPress}
        delayLongPress={500}
      >
        {body}
      </Pressable>
    );
  }

  if (isUser) {
    // User bubble: same Markdown pipeline as assistant — `@mention`
    // serialisation `[MUL-1](mention://issue/<id>)`, inline links, and
    // inline code resolve identically to web's
    // `packages/views/chat/components/chat-message-list.tsx` user branch.
    // Width is capped at 80% so the bubble keeps the iMessage-style
    // trailing alignment instead of stretching across the column.
    const body = (
      <View
        className={cn(
          "self-end max-w-[80%] rounded-2xl border-2 px-3.5 py-2 transition-colors",
          isSelecting
            ? "bg-primary/5 border-primary/30"
            : longPress.isPressed
              ? "bg-muted border-primary/30"
              : "bg-muted border-transparent",
        )}
      >
        <Markdown
          content={message.content}
          attachments={message.attachments}
          selectable={isSelecting}
        />
      </View>
    );
    if (isSelecting) return body;
    return (
      <Pressable
        onLongPress={longPress.onLongPress}
        delayLongPress={500}
      >
        {body}
      </Pressable>
    );
  }

  // Assistant: full-width inside the FlashList's px-4 content container —
  // matches web's `<div className="text-sm leading-relaxed prose prose-sm
  // max-w-none">` which has no width cap of its own and gets its left/
  // right gutter from the outer max-w-4xl px-5 container.
  //
  // No bubble shell here, so neither border nor bg-tint highlight makes
  // sense — tinting a full-width raw row would read as a stray band, and
  // a border baseline would shift layout by 2px on press (B4). Select-mode
  // cue for assistant is the ActionSheet itself plus the markdown becoming
  // selectable; exit via scroll / tab switch / select another message.
  const body = (
    <Markdown
      content={message.content}
      attachments={message.attachments}
      selectable={isSelecting}
    />
  );
  if (isSelecting) return body;
  return (
    <Pressable
      onLongPress={longPress.onLongPress}
      delayLongPress={500}
    >
      {body}
    </Pressable>
  );
}
