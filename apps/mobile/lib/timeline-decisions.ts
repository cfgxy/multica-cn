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

export type TimelineListItem = TimelineRow | DecisionTimelineItem;

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
  return ordered
    .sort((a, b) => a.at - b.at || a.tie - b.tie)
    .map((o) => o.item);
}
