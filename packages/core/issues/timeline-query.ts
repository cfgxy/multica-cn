import type { TimelineEntry } from "@multica/core/types";

// Mirrors server/internal/handler/activity.go:timelineHardCap.
export const TIMELINE_HARD_CAP = 2000;

export type TimelineTruncationKind = "activity" | "comment";

export interface TimelineQueryData {
  entries: TimelineEntry[];
  truncatedKinds: TimelineTruncationKind[];
}

const TRUNCATION_KINDS: readonly TimelineTruncationKind[] = ["activity", "comment"];

export function parseTimelineTruncatedKinds(
  value: string | null,
): TimelineTruncationKind[] {
  if (!value) return [];

  const seen = new Set<TimelineTruncationKind>();
  for (const token of value.split(",")) {
    const kind = token.trim();
    if (TRUNCATION_KINDS.includes(kind as TimelineTruncationKind)) {
      seen.add(kind as TimelineTruncationKind);
    }
  }
  return [...seen];
}

export function timelineEntries(data: TimelineQueryData | undefined): TimelineEntry[] {
  return data?.entries ?? [];
}

/**
 * Updates entries without turning an incremental event into a fabricated
 * full-query snapshot. Only a successful query function may create this cache.
 */
export function updateTimelineEntries(
  previous: TimelineQueryData | undefined,
  update: (entries: TimelineEntry[]) => TimelineEntry[],
): TimelineQueryData | undefined {
  if (!previous) return undefined;
  return {
    ...previous,
    entries: update(previous.entries),
  };
}

function countEntriesByKind(
  entries: readonly TimelineEntry[],
  kind: TimelineTruncationKind,
): number {
  return entries.reduce(
    (count, entry) => count + (entry.type === kind ? 1 : 0),
    0,
  );
}

/**
 * A just-arrived realtime entry can temporarily make the local cache larger
 * than the last server window. Surface that conservatively until a refetch
 * replaces the header snapshot with the authoritative one.
 */
export function effectiveTruncatedKinds(
  data: TimelineQueryData | undefined,
): TimelineTruncationKind[] {
  if (!data) return [];

  const kinds = new Set(data.truncatedKinds);
  for (const kind of TRUNCATION_KINDS) {
    if (countEntriesByKind(data.entries, kind) > TIMELINE_HARD_CAP) {
      kinds.add(kind);
    }
  }
  return [...kinds];
}

export function crossedTimelineHardCap(
  previousEntries: readonly TimelineEntry[],
  nextEntries: readonly TimelineEntry[],
  kind: TimelineTruncationKind,
): boolean {
  return (
    countEntriesByKind(previousEntries, kind) <= TIMELINE_HARD_CAP &&
    countEntriesByKind(nextEntries, kind) > TIMELINE_HARD_CAP
  );
}
