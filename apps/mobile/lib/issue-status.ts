/**
 * Mirror of the BOARD_STATUSES order + status labels from
 * packages/core/issues/config/status.ts.
 *
 * Mirrored, not imported: the source file co-exports `STATUS_CONFIG` with
 * web colour tokens (Tailwind v4 syntax) that mobile must not pull in.
 * Keeping this list owned by mobile keeps the import boundary clean.
 *
 * If web ever reorders BOARD_STATUSES or adds/removes a status, this file
 * must be updated to keep the "Counts and visibility must agree" rule
 * (apps/mobile/CLAUDE.md) intact.
 */
import type { IssuePriority, IssueStatus } from "@multica/core/types";
import i18n from "i18next";

/** Statuses surfaced in list/board views (matches web — `cancelled` excluded). */
export const BOARD_STATUSES: IssueStatus[] = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
];

export function statusLabel(status: IssueStatus): string {
  return i18n.t(`issues:status.${status}`, STATUS_LABEL_EN[status]);
}

export function priorityLabel(priority: IssuePriority): string {
  return i18n.t(`issues:priority.${priority}`, PRIORITY_LABEL_EN[priority]);
}

// English fallbacks used when i18n key is missing.
const STATUS_LABEL_EN: Record<IssueStatus, string> = {
  backlog: "Backlog",
  todo: "Todo",
  in_progress: "In Progress",
  in_review: "In Review",
  done: "Done",
  blocked: "Blocked",
  cancelled: "Cancelled",
};

const PRIORITY_LABEL_EN: Record<IssuePriority, string> = {
  none: "No priority",
  low: "Low",
  medium: "Medium",
  high: "High",
  urgent: "Urgent",
};

/**
 * Static lookup (evaluated once at module load for initial render).
 * After i18n is initialized, components should prefer `statusLabel()`
 * / `priorityLabel()` for locale-reactive labels.
 * Kept for backward compat — callers that don't need i18n yet.
 */
export const STATUS_LABEL: Record<IssueStatus, string> = STATUS_LABEL_EN;

export const PRIORITY_LABEL: Record<IssuePriority, string> = PRIORITY_LABEL_EN;
