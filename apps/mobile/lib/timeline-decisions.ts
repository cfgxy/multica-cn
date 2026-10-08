/**
 * Interleave decision cards (RUYI-345) into the mobile timeline flat list.
 *
 * Mirrors web's merge in `packages/views/issues/components/issue-detail.tsx`
 * (entries + decisions sorted by created_at), but emits the mobile list-item
 * union: timeline rows stay `TimelineRow`s, cards become
 * `DecisionTimelineItem`s. Pure and node-testable — the component only wires
 * the result into FlashList.
 *
 * Consumers that only care about comments (threading, locate, geometry)
 * discriminate via `"decision" in item`; every such consumer already filters
 * on `entry.type === "comment"`, which a decision item's minimal `entry`
 * surface never satisfies.
 */
import type { IssueDecision } from "@multica/core/types";
import type { TimelineRow } from "./timeline-thread";

export interface DecisionTimelineItem {
  decision: IssueDecision;
  /** Minimal ordering surface: FlashList keys and the unread-divider anchor
   *  compare `entry.id` / `entry.created_at`. Deliberately NOT a
   *  TimelineEntry — comment-only consumers must skip decision items. */
  entry: { id: string; created_at: string };
}

// Batch answer bar (RUYI-471): one aggregate row above the first open card
// when the issue has two or more. Carries the same minimal `entry` surface as
// a card (FlashList keys / divider anchor read entry.id), and deliberately
// has no `decision` field so `"decision" in item` keeps excluding it from
// comment-only consumers.
export interface DecisionBatchTimelineItem {
  batchBar: { open: IssueDecision[] };
  entry: { id: string; created_at: string };
}

export type TimelineListItem = TimelineRow | DecisionTimelineItem | DecisionBatchTimelineItem;

export const DECISION_BATCH_BAR_ID = "decision-batch-bar";

export function isDecisionBatchItem(
  item: TimelineListItem,
): item is DecisionBatchTimelineItem {
  return "batchBar" in item;
}

export function isDecisionItem(
  item: TimelineListItem,
): item is DecisionTimelineItem {
  return "decision" in item;
}

export function interleaveDecisions(
  rows: TimelineRow[],
  decisions: readonly IssueDecision[],
): TimelineListItem[] {
  if (decisions.length === 0) return rows;
  // Tie-break: at equal timestamps a timeline row reads before a card
  // (a comment authored in the same instant belongs to the exchange the
  // card answered, not after it). Rows keep their input order.
  const ordered: Array<{ at: number; tie: number; item: TimelineListItem }> =
    rows.map((row, tie) => ({
      at: Date.parse(row.entry.created_at) || 0,
      tie,
      item: row,
    }));
  decisions.forEach((decision, i) => {
    ordered.push({
      at: Date.parse(decision.created_at) || 0,
      tie: rows.length + i, // decisions sort after rows at the same instant
      item: {
        decision,
        entry: { id: decision.id, created_at: decision.created_at },
      },
    });
  });
  const out = ordered
    .sort((a, b) => a.at - b.at || a.tie - b.tie)
    .map((o) => o.item);
  // Mirrors web's issue-detail placement (RUYI-534): with two or more open
  // cards the bar lands at the very end of the timeline, appended after the
  // sort so timestamps never move it. The cards themselves stay ordered by
  // created_at, which is what the server's "1A 2B" numbering reads.
  // Server numbering ("1A 2B" binding) is created_at ASC over open cards —
  // sort locally so the bar's row order and anchor never depend on the
  // caller's array order.
  const open = decisions
    .filter((d) => d.status === "open")
    .sort(
      (a, b) =>
        Date.parse(a.created_at) - Date.parse(b.created_at) ||
        (a.id < b.id ? -1 : 1),
    );
  if (open.length >= 2) {
    out.push({
      batchBar: { open },
      entry: { id: DECISION_BATCH_BAR_ID, created_at: open[0]!.created_at },
    });
  }
  return out;
}
