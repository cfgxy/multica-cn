import type { TimelineEntry } from "@multica/core/types";

export function compareTimelineEntriesAsc(a: TimelineEntry, b: TimelineEntry): number {
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

export type TimelineSortMode = "recent-comment" | "created";

export interface TimelineModelStats {
  threadBlockCount: number;
  activityEntryCount: number;
  activityVisualGroupCount: number;
  sortableBlockCount: number;
}

export interface TimelineModel {
  entries: TimelineEntry[];
  stats: TimelineModelStats;
}

type TimelineBlock = {
  key: string;
  entries: TimelineEntry[];
  sortEntry: TimelineEntry;
};

const COALESCE_MS = 2 * 60 * 1000;
const NO_TIME_LIMIT_ACTIONS = new Set(["task_completed", "task_failed"]);
const NEVER_COALESCE_ACTIONS = new Set(["squad_leader_evaluated"]);

function resolveTimelineBlocks(
  entries: readonly TimelineEntry[],
  mode: TimelineSortMode,
): { blocks: TimelineBlock[]; threadBlockCount: number; activityEntryCount: number } {
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

  const threadEntries = new Map<string, TimelineEntry[]>();
  const activityBlocks: TimelineBlock[] = [];

  for (const entry of entries) {
    if (entry.type !== "comment") {
      activityBlocks.push({
        key: `activity:${entry.id}`,
        entries: [entry],
        sortEntry: entry,
      });
      continue;
    }

    const rootId = resolveRootId(entry.id);
    const block = threadEntries.get(rootId) ?? [];
    block.push(entry);
    threadEntries.set(rootId, block);
  }

  const threadBlocks = [...threadEntries.entries()].map(([rootId, blockEntries]) => {
    const orderedEntries = sortTimelineEntriesAsc([...blockEntries]);
    const root = commentsById.get(rootId) ?? orderedEntries[0]!;
    const latest = orderedEntries[orderedEntries.length - 1]!;
    return {
      key: `comment:${rootId}`,
      entries: orderedEntries,
      sortEntry: mode === "created" ? root : latest,
    };
  });

  return {
    blocks: [...threadBlocks, ...activityBlocks],
    threadBlockCount: threadBlocks.length,
    activityEntryCount: activityBlocks.filter((block) => block.entries[0]?.type === "activity").length,
  };
}

function coalesceActivities(entries: readonly TimelineEntry[]): TimelineEntry[] {
  const coalesced: TimelineEntry[] = [];
  for (const entry of entries) {
    if (entry.type === "activity") {
      const previous = coalesced[coalesced.length - 1];
      if (
        !NEVER_COALESCE_ACTIONS.has(entry.action ?? "") &&
        previous?.type === "activity" &&
        previous.action === entry.action &&
        previous.actor_type === entry.actor_type &&
        previous.actor_id === entry.actor_id &&
        (NO_TIME_LIMIT_ACTIONS.has(entry.action ?? "") ||
          Math.abs(
            new Date(entry.created_at).getTime() - new Date(previous.created_at).getTime(),
          ) <= COALESCE_MS)
      ) {
        coalesced[coalesced.length - 1] = {
          ...entry,
          coalesced_count: (previous.coalesced_count ?? 1) + 1,
        };
        continue;
      }
    }
    coalesced.push(entry);
  }
  return coalesced;
}

function countActivityVisualGroups(entries: readonly TimelineEntry[]): number {
  let groups = 0;
  let previousWasActivity = false;
  for (const entry of entries) {
    const isActivity = entry.type === "activity";
    if (isActivity && !previousWasActivity) groups += 1;
    previousWasActivity = isActivity;
  }
  return groups;
}

/**
 * Produces the one display model shared by every client. The input is the raw
 * query cache only: threading, sorting, activity coalescing, and statistics
 * happen here in a fixed order so renderers cannot drift independently.
 */
export function buildTimelineModel(
  entries: readonly TimelineEntry[],
  mode: TimelineSortMode,
): TimelineModel {
  const { blocks, threadBlockCount, activityEntryCount } = resolveTimelineBlocks(entries, mode);
  const ordered = blocks
    .sort(
      (a, b) =>
        compareTimelineEntriesAsc(a.sortEntry, b.sortEntry) ||
        (a.key === b.key ? 0 : a.key < b.key ? -1 : 1),
    )
    .flatMap((block) => block.entries);
  const displayEntries = coalesceActivities(ordered);

  return {
    entries: displayEntries,
    stats: {
      threadBlockCount,
      activityEntryCount,
      activityVisualGroupCount: countActivityVisualGroups(displayEntries),
      sortableBlockCount: threadBlockCount + activityEntryCount,
    },
  };
}

export function latestThreadComment(
  entries: readonly TimelineEntry[],
): TimelineEntry | undefined {
  return entries
    .filter((entry) => entry.type === "comment")
    .reduce<TimelineEntry | undefined>(
      (latest, entry) =>
        !latest || compareTimelineEntriesAsc(latest, entry) < 0 ? entry : latest,
      undefined,
    );
}
