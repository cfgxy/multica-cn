import type { TimelineEntry } from "@multica/core/types";

function compareTimelineEntriesAsc(a: TimelineEntry, b: TimelineEntry): number {
  if (a.created_at !== b.created_at) {
    return a.created_at < b.created_at ? -1 : 1;
  }
  if (a.id === b.id) return 0;
  return a.id < b.id ? -1 : 1;
}

/**
 * Stable-ascending sort for flat TimelineEntry[] caches.
 *
 * All writers that append to an issue timeline cache MUST pass through
 * this helper so the display order stays `created_at` ASC (id tie-breaker)
 * even when WebSocket events and mutation onSuccess callbacks arrive
 * out of chronological order.
 *
 * Observers that mutate in place (map / filter by id) don't need this —
 * they preserve the existing relative order.
 */
export function sortTimelineEntriesAsc(entries: TimelineEntry[]): TimelineEntry[] {
  entries.sort(compareTimelineEntriesAsc);
  return entries;
}

/**
 * Order a threaded timeline by each thread's latest comment.
 *
 * Replies render inside their root card, so ordering root cards by the root's
 * own timestamp can put a stale thread after a thread that stayed active much
 * later. Treat each comment thread as one display block, rank that block by its
 * newest comment, and keep comments inside the block chronological. Activities
 * remain single-entry blocks ranked by their own timestamp.
 */
export function sortTimelineEntriesForThreadedDisplay(
  entries: readonly TimelineEntry[],
): TimelineEntry[] {
  const commentsById = new Map(
    entries.filter((entry) => entry.type === "comment").map((entry) => [entry.id, entry]),
  );
  const rootByCommentId = new Map<string, string>();
  const resolving = new Set<string>();

  const resolveRootId = (commentId: string): string => {
    const cached = rootByCommentId.get(commentId);
    if (cached) return cached;

    const comment = commentsById.get(commentId);
    if (!comment?.parent_id || !commentsById.has(comment.parent_id)) {
      rootByCommentId.set(commentId, commentId);
      return commentId;
    }
    if (resolving.has(commentId)) return commentId;

    resolving.add(commentId);
    const rootId = resolveRootId(comment.parent_id);
    resolving.delete(commentId);
    rootByCommentId.set(commentId, rootId);
    return rootId;
  };

  type DisplayBlock = {
    key: string;
    entries: TimelineEntry[];
    latest: TimelineEntry;
  };
  const blocks = new Map<string, DisplayBlock>();

  for (const entry of entries) {
    const key =
      entry.type === "comment"
        ? `comment:${resolveRootId(entry.id)}`
        : `activity:${entry.id}`;
    const block = blocks.get(key);
    if (!block) {
      blocks.set(key, { key, entries: [entry], latest: entry });
      continue;
    }
    block.entries.push(entry);
    if (compareTimelineEntriesAsc(block.latest, entry) < 0) {
      block.latest = entry;
    }
  }

  return [...blocks.values()]
    .sort(
      (a, b) =>
        compareTimelineEntriesAsc(a.latest, b.latest) ||
        (a.key === b.key ? 0 : a.key < b.key ? -1 : 1),
    )
    .flatMap((block) => sortTimelineEntriesAsc(block.entries));
}
