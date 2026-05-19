/**
 * Chat message list — user / assistant bubbles, oldest at top, newest at
 * bottom. Auto-scrolls to the bottom when the list length increases (new
 * message arrived or optimistic send seeded the cache).
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
 *     markdown renderer. See plan-velvety-puddle.md "v2 follow-up".
 *
 * Layout uses a plain FlatList (mobile baseline — no FlashList — see
 * `components/issue/timeline-list.tsx:7`).
 */
import { useEffect, useRef } from "react";
import { ActivityIndicator, FlatList, View } from "react-native";
import type { ChatMessage } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { Markdown } from "@/lib/markdown";
import { failureReasonLabel } from "@/lib/failure-reason-label";

interface Props {
  messages: ChatMessage[];
  loading: boolean;
}

export function ChatMessageList({ messages, loading }: Props) {
  const listRef = useRef<FlatList<ChatMessage>>(null);
  const lastLenRef = useRef(messages.length);
  // Sticky-bottom state. `isAtBottomRef` is updated on every scroll;
  // `userHasScrolledRef` is set on the first scroll event for the current
  // session and cleared on session switch. Together they encode "should
  // the next content-size change pull us to the bottom?".
  //
  // WHY this exists: assistant Markdown is rendered asynchronously (Shiki
  // syntax highlighting, image natural-size resolution, lightbox provider
  // injection). Each async layout completion fires onContentSizeChange.
  // Without these refs the previous implementation unconditionally called
  // scrollToEnd on every content-size change, so the user could not stay
  // anywhere above the bottom — every drag-up was immediately snapped
  // back as the next async render landed. Pattern mirrors web's chat
  // window stick-to-bottom logic (packages/views/chat/components/chat-
  // window.tsx).
  const isAtBottomRef = useRef(true);
  const userHasScrolledRef = useRef(false);
  const firstMsgIdRef = useRef<string | null>(null);

  // Session-switch detection. The parent reuses this component instance
  // across sessions, only swapping the `messages` prop. When the first
  // message id changes, the parent switched sessions — reset sticky
  // state so the new session lands at its bottom even if we were
  // reading history in the previous one.
  useEffect(() => {
    const newFirst = messages[0]?.id ?? null;
    if (newFirst === firstMsgIdRef.current) return;
    firstMsgIdRef.current = newFirst;
    isAtBottomRef.current = true;
    userHasScrolledRef.current = false;
    lastLenRef.current = messages.length;
    // Layout-after-render: let FlatList measure new content first.
    const id = setTimeout(() => {
      listRef.current?.scrollToEnd({ animated: false });
    }, 0);
    return () => clearTimeout(id);
  }, [messages]);

  // New-message auto-scroll: only when the user is anchored at the
  // bottom. Reading history → new arrivals don't yank you down. Same
  // semantic as iMessage / web ChatWindow.
  useEffect(() => {
    const grew = messages.length > lastLenRef.current;
    lastLenRef.current = messages.length;
    if (!grew) return;
    if (!isAtBottomRef.current) return;
    const id = setTimeout(() => {
      listRef.current?.scrollToEnd({ animated: true });
    }, 0);
    return () => clearTimeout(id);
  }, [messages.length]);

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
    <FlatList
      ref={listRef}
      data={messages}
      keyExtractor={(m) => m.id}
      renderItem={({ item }) => <MessageRow message={item} />}
      // flex-1 mirrors web's `<div className="flex-1 overflow-y-auto">`
      // pattern — without it, FlatList in a multi-child flex column
      // (KeyboardAvoidingView with siblings StatusPill + ChatComposer)
      // can compute its height as content-height rather than the
      // remaining viewport, leaving no overflow to scroll.
      className="flex-1"
      // Padding mirrors web's outer message container
      // (`max-w-4xl px-5 py-4 space-y-4` in
      // packages/views/chat/components/chat-message-list.tsx). px-4 gives
      // assistant (full-width) markdown room to breathe; pb-4 keeps the
      // last bubble from kissing the composer; gap-3 between bubbles
      // matches web's space-y-4 visual rhythm at mobile scale.
      contentContainerClassName="px-4 pt-3 pb-4 gap-3"
      // iMessage-style keyboard dismissal: dragging the list pulls the
      // keyboard down with the finger (iOS), and tapping any empty
      // space between bubbles dismisses it. `handled` keeps Pressables
      // inside bubbles (long-press action sheet etc.) firing normally.
      keyboardDismissMode="interactive"
      keyboardShouldPersistTaps="handled"
      onScroll={(e) => {
        userHasScrolledRef.current = true;
        const { contentOffset, contentSize, layoutMeasurement } = e.nativeEvent;
        const distFromBottom =
          contentSize.height - (contentOffset.y + layoutMeasurement.height);
        // 80px tolerance — users hovering just above the bottom while
        // reading a long reply still count as "at bottom" so the next
        // message follows them.
        isAtBottomRef.current = distFromBottom < 80;
      }}
      scrollEventThrottle={16}
      onContentSizeChange={() => {
        if (messages.length === 0) return;
        // Before the user has touched the list, every content-size
        // change snaps to bottom — async markdown rendering shouldn't
        // leave us mid-list on first paint of a session.
        // After first interaction, only re-stick if the user is still
        // anchored at the bottom. This is the guard that fixes the
        // "list locks at bottom; can't scroll up while Shiki/Image
        // finish rendering" bug.
        if (!userHasScrolledRef.current || isAtBottomRef.current) {
          listRef.current?.scrollToEnd({ animated: false });
        }
      }}
    />
  );
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
    // User bubble: muted gray background + foreground text — mirrors
    // web's `<div className="rounded-2xl bg-muted px-3.5 py-2 text-sm
    // max-w-[80%] break-words">` in packages/views/chat/components/
    // chat-message-list.tsx. Mention serialisation `[MUL-1](mention://…)`
    // still shows as raw markdown text here; this is the explicit v1
    // trade-off (see plan-velvety-puddle.md). Assistant messages go
    // through the rich Markdown pipeline below.
    return (
      <View className="self-end max-w-[80%] rounded-2xl bg-muted px-3.5 py-2">
        <Text className="text-sm text-foreground" selectable>
          {message.content}
        </Text>
      </View>
    );
  }

  // Assistant: full-width inside the FlatList's px-4 content container —
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
