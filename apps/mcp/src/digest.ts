/**
 * Pure aggregation for the `progress_digest` tool.
 *
 * The digest is composed client-side from cheap REST calls: one
 * `limit=1`-per-status total probe and two small sorted lists. All
 * interpretation (what counts as open/overdue) lives here so it is unit
 * testable against a fixed clock.
 */

import type { IssueInfo } from "./types.js";

/** Statuses that close an issue; overdue never applies to them. */
const TERMINAL_STATUSES = new Set(["done", "cancelled"]);

export const DIGEST_TRACKED_STATUSES = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "blocked",
] as const;

export interface DigestCountsInput {
  status: string;
  total: number;
}

export interface DigestIssueRef {
  identifier: string;
  title: string;
  status: string;
  due_date?: string;
  last_activity_at?: string;
}

export interface ProgressDigest {
  workspace: string;
  generated_at: string;
  open_total: number;
  counts_by_status: Record<string, number>;
  overdue: DigestIssueRef[];
  due_soon: DigestIssueRef[];
  recently_active: DigestIssueRef[];
  notes: string[];
}

export interface DigestInput {
  workspace: string;
  generatedAt: Date;
  counts: DigestCountsInput[];
  recentlyActive: IssueInfo[];
  dueQueue: IssueInfo[];
}

function toRef(issue: IssueInfo): DigestIssueRef {
  return {
    identifier: issue.identifier,
    title: issue.title,
    status: issue.status,
    due_date: issue.due_date,
    last_activity_at: issue.last_activity_at,
  };
}

export function buildDigest(input: DigestInput): ProgressDigest {
  const today = input.generatedAt.toISOString().slice(0, 10);

  const countsByStatus: Record<string, number> = {};
  let openTotal = 0;
  for (const entry of input.counts) {
    countsByStatus[entry.status] = entry.total;
    if (!TERMINAL_STATUSES.has(entry.status)) {
      openTotal += entry.total;
    }
  }

  const overdue: DigestIssueRef[] = [];
  const dueSoon: DigestIssueRef[] = [];
  for (const issue of input.dueQueue) {
    if (!issue.due_date || TERMINAL_STATUSES.has(issue.status)) {
      continue;
    }
    // The queue is sorted by due date ascending, so everything before the
    // first non-overdue entry is overdue; the rest is upcoming.
    if (issue.due_date < today) {
      overdue.push(toRef(issue));
    } else {
      dueSoon.push(toRef(issue));
    }
  }

  const notes: string[] = [];
  if (input.dueQueue.length > 0 && overdue.length === input.dueQueue.length) {
    notes.push(
      "Overdue list may be truncated: the due-date queue window filled up with overdue issues.",
    );
  }

  return {
    workspace: input.workspace,
    generated_at: input.generatedAt.toISOString(),
    open_total: openTotal,
    counts_by_status: countsByStatus,
    overdue,
    due_soon: dueSoon,
    recently_active: input.recentlyActive.map(toRef),
    notes,
  };
}
