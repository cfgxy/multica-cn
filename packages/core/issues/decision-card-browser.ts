import type { IssueDecision } from "../types";

// RUYI-863: shared position logic for the issue decision-card browser. The
// three entries (info-feed single card, batch entry, more-menu entry) all
// open the SAME previous/next browser; entry parameters pick the initial
// card and filter only. Web/desktop render it in a dialog, mobile in a
// screen — the state rules live here so both platforms cannot drift.

export type DecisionCardFilter = "all" | "open" | "answered" | "cancelled";

export const DECISION_CARD_FILTERS: readonly DecisionCardFilter[] = [
  "all",
  "open",
  "answered",
  "cancelled",
] as const;

// Authorization cards (RUYI-630) never sit in the "open" bucket: their
// lifecycle lives on the decision request, and the restore endpoint
// rejects them with 409. "all" still shows them for history; only the
// status filters exclude them.
function matchesFilter(
  decision: IssueDecision,
  filter: DecisionCardFilter,
): boolean {
  if (filter === "all") return true;
  if (filter === "open") {
    return (
      decision.status === "open" && decision.decision_kind !== "authorization"
    );
  }
  return decision.status === filter;
}

/**
 * The browser's row set for a filter. "all" is the card's natural order
 * (created_at ASC from the list endpoint); the status filters keep that
 * order so navigation "next" always means "newer card".
 */
export function filterDecisionCards(
  decisions: readonly IssueDecision[],
  filter: DecisionCardFilter,
): readonly IssueDecision[] {
  return decisions.filter((decision) => matchesFilter(decision, filter));
}

/**
 * Initial position for an entry:
 * - single-card entry: pin the target card id (any status — "all" filter,
 *   so an already-answered or cancelled card never bounces elsewhere).
 * - batch entry: no pin, filter "open" → the FIRST pending card.
 * - more-menu entry: no pin, filter "all" → the first card.
 */
export function initialDecisionBrowserCardId(
  decisions: readonly IssueDecision[],
  filter: DecisionCardFilter,
  initialCardId?: string | null,
): string | null {
  const filtered = filterDecisionCards(decisions, filter);
  if (filtered.length === 0) return null;
  if (initialCardId && filtered.some((d) => d.id === initialCardId)) {
    return initialCardId;
  }
  return filtered[0]?.id ?? null;
}

/**
 * Position resolution after the underlying data or the filter changes.
 *
 * - The desired card still matches the filter → stay on it (the "land on
 *   the target card" rule — no surprise navigation, RUYI-863 single-card
 *   entry).
 * - The desired card has LEFT the active filter's bucket (answered while
 *   filtering open, restored while filtering cancelled) → advance to the
 *   next card AFTER it in creation order that matches; wrap to the first
 *   matching card when nothing is left after. Sequential batch answering
 *   therefore walks the pending list card by card without leaving the
 *   browser; when the filtered set empties the caller renders the
 *   done/empty state instead of bouncing back to the feed.
 *
 * Returns null when the filter matches no card at all.
 */
export function resolveDecisionBrowserPosition(
  decisions: readonly IssueDecision[],
  filter: DecisionCardFilter,
  desiredCardId: string | null,
): string | null {
  const filtered = filterDecisionCards(decisions, filter);
  if (filtered.length === 0) return null;
  if (desiredCardId && filtered.some((d) => d.id === desiredCardId)) {
    return desiredCardId;
  }
  if (!desiredCardId || !decisions.some((d) => d.id === desiredCardId)) {
    return filtered[0]?.id ?? null;
  }
  const start = decisions.findIndex((d) => d.id === desiredCardId);
  for (let i = start + 1; i < decisions.length; i++) {
    const card = decisions[i];
    if (card && matchesFilter(card, filter)) return card.id ?? null;
  }
  // The desired card was the last match — wrap to the first remaining one.
  return filtered[0]?.id ?? null;
}

/** Previous/next targets within the active filter, clamped to the edges. */
export function decisionBrowserNeighbor(
  decisions: readonly IssueDecision[],
  filter: DecisionCardFilter,
  currentCardId: string | null,
  direction: "previous" | "next",
): string | null {
  const filtered = filterDecisionCards(decisions, filter);
  const index = filtered.findIndex((d) => d.id === currentCardId);
  if (index === -1) return null;
  const neighbor = filtered[direction === "previous" ? index - 1 : index + 1];
  return neighbor?.id ?? null;
}
