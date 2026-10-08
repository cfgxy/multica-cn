/**
 * Decision Center list shaping (RUYI-494) — the mobile mirror of
 * packages/views/decisions/components/decision-center-page.tsx's section
 * grouping. Pure and node-testable; the screen only wires the result into
 * SectionList.
 *
 * Behavioral parity (apps/mobile/CLAUDE.md "Counts and visibility must
 * agree"):
 *   - One row per CARD — never folded by issue. The IA is fixed by Owner
 *     correction on RUYI-494: same Issue with N cards renders N rows, line 1
 *     = identifier + title, line 2 = that card's question. The server
 *     returns one item per card; nothing here merges them.
 *   - Section order is open → answered → cancelled (待决策 first); within a
 *     section the server's newest-first order passes through untouched.
 *   - Zero-row sections are dropped from the rendered list, but a section's
 *     `count` is the server's workspace total (data.counts), not rows.length
 *     — the server list endpoint carries no pagination today, and keeping
 *     the server number as the single source means the badge, the section
 *     header and web can never disagree.
 */
import type {
  IssueDecision,
  WorkspaceDecisionInbox,
  WorkspaceDecisionInboxItem,
} from "@multica/core/types";

export type DecisionInboxSectionKey = IssueDecision["status"];

export const DECISION_SECTION_ORDER = [
  "open",
  "answered",
  "cancelled",
] as const satisfies readonly DecisionInboxSectionKey[];

/**
 * TAB dimension of the Decisions tab (RUYI-530) — mirrors the Tasks tab's
 * "全部 + one pill per window" shape; each non-all pill maps 1:1 to a card
 * status (待决策/已决策/已失效, same labels as the section headers).
 */
export const DECISION_TAB_ORDER = [
  "all",
  "open",
  "answered",
  "cancelled",
] as const;

export type DecisionTab = (typeof DECISION_TAB_ORDER)[number];

/**
 * Filter toggle dimensions (RUYI-530). Status belongs to the TAB itself
 * (same division as Tasks: the tab owns the status window), so the sheet
 * only carries the two card-owned dimensions:
 *   - recommendedOnly → the 「有推荐」 badge flag
 *   - agentCreatedOnly → the card was raised by an agent, not a member
 */
export interface DecisionViewFilter {
  tab: DecisionTab;
  recommendedOnly: boolean;
  agentCreatedOnly: boolean;
}

/**
 * Actor type for the row avatar (RUYI-530). The card's `created_by_type`
 * is a free string server-side; anything but the known polymorphs resolves
 * to `system` so ActorAvatar always renders a real glyph (initials/icon
 * fallback) instead of a blank slot.
 */
export type DecisionActorType = "member" | "agent" | "system";

export function resolveDecisionActorType(
  rawType: string | null | undefined,
): DecisionActorType {
  if (rawType === "member" || rawType === "agent") return rawType;
  return "system";
}

/**
 * Graying predicate (RUYI-530): answered/cancelled rows render the inbox's
 * read style; open rows keep full contrast (待决策不灰化 — negative-
 * asserted in tests).
 */
export function isDecidedDecisionRow(
  status: DecisionInboxSectionKey,
): boolean {
  return status !== "open";
}

/** Render-ready row: fixed two lines (identifier+title / question). */
export interface DecisionInboxRow {
  id: string;
  issueId: string;
  /** `RUYI-494` — always visible per the IA; `#<number>` fallback when the
   *  server row predates the identifier join (defensive; falls in the same
   *  slot web's decision-inbox-row renders). */
  identifier: string;
  issueTitle: string;
  /** The card's own question — line 2. Two cards on one issue differ here. */
  question: string;
  /** Card carries a recommendation → 「有推荐」 badge (AC10). */
  recommended: boolean;
  status: DecisionInboxSectionKey;
  createdAt: string;
  /** Card creator identity — the row avatar's data (RUYI-530). */
  createdByType: string;
  createdById: string;
}

export interface DecisionInboxSection {
  key: DecisionInboxSectionKey;
  rows: DecisionInboxRow[];
  /** Workspace total from the server counts, not rows.length. */
  count: number;
}

export function toDecisionInboxRow(
  item: WorkspaceDecisionInboxItem,
): DecisionInboxRow {
  return {
    id: item.id,
    issueId: item.issue_id,
    identifier: item.issue_identifier || `#${item.issue_number}`,
    issueTitle: item.issue_title,
    question: item.question,
    recommended: item.recommended_indices.length > 0,
    status: item.status,
    createdAt: item.created_at,
    createdByType: item.created_by_type,
    createdById: item.created_by_id,
  };
}

/**
 * TAB + filter narrowing (RUYI-530). The TAB owns the status window (all →
 * no status filter; otherwise exactly one card status); the two sheet
 * toggles compose with it as AND. Server order passes through untouched —
 * this only drops rows, never reorders.
 */
export function filterDecisionRows(
  rows: DecisionInboxRow[],
  view: DecisionViewFilter,
): DecisionInboxRow[] {
  return rows.filter(
    (row) =>
      (view.tab === "all" || row.status === view.tab) &&
      (!view.recommendedOnly || row.recommended) &&
      (!view.agentCreatedOnly ||
        resolveDecisionActorType(row.createdByType) === "agent"),
  );
}

export function groupDecisionInboxSections(
  inbox: WorkspaceDecisionInbox,
): DecisionInboxSection[] {
  const rows = inbox.items.map(toDecisionInboxRow);
  return DECISION_SECTION_ORDER.map((key) => ({
    key,
    rows: rows.filter((row) => row.status === key),
    count: inbox.counts[key],
  })).filter((section) => section.rows.length > 0);
}
