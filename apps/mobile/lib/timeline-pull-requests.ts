/**
 * Interleave linked pull-request cards (RUYI-634) into the mobile timeline
 * feed — the PR counterpart of lib/timeline-decisions.ts.
 *
 * Runs AFTER interleaveDecisions on the already-merged feed: each card is
 * inserted at its `pr_created_at` position without re-sorting the existing
 * items, so the decisions' own invariants survive untouched — notably the
 * trailing decision batch bar (RUYI-534: always the very last row), which
 * this pass treats as a +∞ sentinel because its `entry.created_at` mirrors
 * the first open card and is NOT its position. Pure and node-testable — the
 * component only wires the result into FlashList.
 *
 * Consumers that only care about comments (threading, locate, geometry)
 * discriminate via `"decision" in item` + `entry.type === "comment"`; a PR
 * item satisfies neither, exactly like a decision item.
 */
import type { GitHubPullRequest } from "@multica/core/types";
import type { TimelineListItem } from "./timeline-decisions";

export interface PullRequestTimelineItem {
  pullRequest: GitHubPullRequest;
  /** Minimal ordering surface — same contract as DecisionTimelineItem.entry:
   *  FlashList keys and the unread-divider anchor read entry.id/created_at,
   *  and comment-only consumers skip items without a comment entry.type. */
  entry: { id: string; created_at: string };
}

/** The full rendered feed: timeline rows + decisions (+ batch bar) + PRs. */
export type TimelineFeedItem = TimelineListItem | PullRequestTimelineItem;

/** Feed-row id prefix for PR cards — keeps PR ids out of comment/decision
 *  uuid space in keyExtractor and the debug view hierarchy. */
export const PULL_REQUEST_ITEM_ID_PREFIX = "pull-request-";

export function isPullRequestItem(
  item: TimelineFeedItem,
): item is PullRequestTimelineItem {
  return "pullRequest" in item;
}

export function interleavePullRequests(
  items: TimelineListItem[],
  prs: readonly GitHubPullRequest[],
): TimelineFeedItem[] {
  if (prs.length === 0) return items;
  const out: TimelineFeedItem[] = [...items];
  for (const pr of prs) {
    out.splice(
      insertIndex(out, Date.parse(pr.pr_created_at) || 0),
      0,
      {
        pullRequest: pr,
        entry: {
          id: `${PULL_REQUEST_ITEM_ID_PREFIX}${pr.id}`,
          created_at: pr.pr_created_at,
        },
      },
    );
  }
  return out;
}

/**
 * Insertion point for a card timestamped `atMs`: after every item with an
 * earlier-or-equal `created_at` (rows read before cards on ties — the same
 * tie-break interleaveDecisions uses), and never after a batch bar.
 */
function insertIndex(items: TimelineFeedItem[], atMs: number): number {
  let idx = 0;
  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;
    if ("batchBar" in item) break;
    if ((Date.parse(item.entry.created_at) || 0) <= atMs) idx = i + 1;
  }
  return idx;
}
