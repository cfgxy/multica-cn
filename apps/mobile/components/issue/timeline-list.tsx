/**
 * The scrolling timeline. ASC chronological — oldest at top, newest near the
 * bottom (above the composer). Pull-to-refresh refetches issue + timeline.
 *
 * Backend returns the full timeline in one shot (server-side pagination
 * was dropped in #2322 — p99 ~30 entries per issue, cursor walking only
 * created bugs at reply-thread boundaries). The previous "Pull to load
 * older" UX and top-edge `fetchOlder` trigger are gone.
 *
 * Inbox deep-link: when `highlightCommentId` is set (paired with a fresh
 * `highlightNonce` per tap), this list auto-scrolls to the matching row
 * and lights up the `<CommentCard>` for ~3.2s. Mirrors the web behavior
 * at packages/views/issues/components/issue-detail.tsx:686-709.
 *
 * Uses native FlatList (mobile baseline doesn't include FlashList — see
 * apps/mobile/CLAUDE.md "Tech-stack baseline"). For the issue volumes the
 * product targets, FlatList is fine.
 */
import { useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, FlatList, RefreshControl, View } from "react-native";
import type { Issue, TimelineEntry } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { IssueHeaderCard } from "./issue-header-card";
import { IssueDescription } from "./issue-description";
import { IssueReactionRow } from "./issue-reaction-row";
import { ActivityRow } from "./activity-row";
import { CommentCard } from "./comment-card";
import { coalesceTimeline } from "@/lib/timeline-coalesce";
import { buildTimelineRows, type TimelineRow } from "@/lib/timeline-thread";

interface Props {
  issue: Issue;
  entries: TimelineEntry[] | undefined;
  timelineLoading: boolean;
  refreshing: boolean;
  onRefresh: () => void;
  /** Long-press → Reply on a comment bubbles up via this callback. The
   *  issue page lifts replyingTo state and feeds it back into the composer. */
  onReplyTo: (commentId: string, name: string) => void;
  /** Inbox deep-link target. Root comment id OR reply id — replies live
   *  inline inside their parent's CommentCard, so a reply target scrolls
   *  to the parent's row and the card highlights the matching child. */
  highlightCommentId?: string;
  /** Per-tap nonce. Re-tapping the same inbox row produces the same
   *  `highlightCommentId` but a fresh nonce, which re-triggers the
   *  scroll-and-flash effect (without this, identical props short-circuit). */
  highlightNonce?: string;
}

/** How long the flash stays "claimed" before we let a new highlight take
 *  over. The fade-out itself is driven by the Reanimated sequence inside
 *  CommentCard; this is just the upstream gate. */
const HIGHLIGHT_HOLD_MS = 2500;

export function TimelineList({
  issue,
  entries,
  timelineLoading,
  refreshing,
  onRefresh,
  onReplyTo,
  highlightCommentId,
  highlightNonce,
}: Props) {
  // Server already returns ASC oldest-first. Pipeline:
  //   1. coalesceTimeline → merge consecutive identical activities
  //   2. buildTimelineRows → reorder so replies sit adjacent to their parent
  //      and tag each reply with `replyTo` for the card to render the
  //      "↪ Replying to" header + thread-line border. This is the mobile
  //      flat-list interpretation of web's recursive reply tree.
  const data = useMemo<TimelineRow[]>(() => {
    if (!entries) return [];
    return buildTimelineRows(coalesceTimeline(entries));
  }, [entries]);

  const listRef = useRef<FlatList<TimelineRow>>(null);
  // Gates single-shot per (commentId, nonce) tuple. Re-tap from inbox
  // bumps the nonce → ref no longer matches → effect re-fires.
  const didHighlightRef = useRef<string | null>(null);
  const [highlightedId, setHighlightedId] = useState<string | null>(null);

  useEffect(() => {
    if (!highlightCommentId || data.length === 0) return;
    const stamp = `${highlightCommentId}:${highlightNonce ?? ""}`;
    if (didHighlightRef.current === stamp) return;

    // Replies are folded into their parent's CommentCard (no separate row);
    // a reply deep-link scrolls to the parent and the card animates the
    // matching child View. Mirrors web's replyToRoot fallback at
    // packages/views/issues/components/issue-detail.tsx:588-607.
    const idx = data.findIndex(
      (r) =>
        r.entry.id === highlightCommentId ||
        r.replies.some((rp) => rp.id === highlightCommentId),
    );
    if (idx < 0) return;

    didHighlightRef.current = stamp;
    listRef.current?.scrollToIndex({
      index: idx,
      animated: true,
      viewPosition: 0.3,
    });
    setHighlightedId(highlightCommentId);
    const t = setTimeout(() => setHighlightedId(null), HIGHLIGHT_HOLD_MS);
    return () => clearTimeout(t);
  }, [highlightCommentId, highlightNonce, data]);

  const ListHeader = (
    <View>
      <IssueHeaderCard issue={issue} />
      <IssueDescription issueId={issue.id} description={issue.description} />
      <IssueReactionRow issue={issue} />
      <View className="px-4 pt-4 pb-2 border-t border-border">
        <Text className="text-xs uppercase tracking-wider text-muted-foreground font-medium">
          Activity
        </Text>
      </View>
      {timelineLoading && (!entries || entries.length === 0) ? (
        <View className="py-6 items-center">
          <ActivityIndicator />
        </View>
      ) : null}
    </View>
  );

  return (
    <FlatList
      ref={listRef}
      data={data}
      keyExtractor={(row) => row.entry.id}
      ListHeaderComponent={ListHeader}
      renderItem={({ item }) =>
        item.entry.type === "comment" ? (
          <CommentCard
            entry={item.entry}
            replies={item.replies}
            issueId={issue.id}
            onReplyTo={onReplyTo}
            highlightedCommentId={highlightedId}
          />
        ) : (
          <ActivityRow entry={item.entry} />
        )
      }
      // Variable row heights + no getItemLayout = scrollToIndex on a target
      // outside the windowed render area throws this callback. Standard RN
      // dance: jump to an estimate, then retry on the next frame once the
      // target's window has rendered. The estimate ignores the header, but
      // viewPosition: 0.3 on retry corrects for it.
      onScrollToIndexFailed={(info) => {
        listRef.current?.scrollToOffset({
          offset: info.averageItemLength * info.index,
          animated: false,
        });
        requestAnimationFrame(() => {
          listRef.current?.scrollToIndex({
            index: info.index,
            animated: true,
            viewPosition: 0.3,
          });
        });
      }}
      refreshControl={
        <RefreshControl refreshing={refreshing} onRefresh={onRefresh} />
      }
      // gap-3 between every row gives uniform 12px spacing — matches web's
      // `<div className="mt-4 flex flex-col gap-3">` outer container. With
      // this owning the spacing, the row components themselves drop their
      // own py so we don't double-up vertical breathing room.
      contentContainerClassName="pb-4 gap-3"
    />
  );
}
