/**
 * RUYI-78 re-entry collapse policy — which root threads stay expanded
 * when the user re-enters an Issue they have opened before.
 *
 * Owner's target behavior (RUYI-78 acceptance ①): on re-entry, each
 * agent's chronologically LAST existing comment must be visible and
 * every other existing comment collapsed. Extracted from
 * timeline-list.tsx as a framework-free pure function so the node vitest
 * lane can test it (same rationale as lib/comment-locate.ts — mobile
 * ships no RN component renderer).
 *
 * Rule (root granularity — the only collapsible unit on mobile, one
 * bubble per thread per apps/mobile/CLAUDE.md): scan every comment in
 * the timeline — root or reply, a reply lives inside its host root's
 * bubble — and for each AGENT actor (`actor_type === "agent"`, keyed by
 * `actor_id`) keep their latest comment. A root stays expanded iff it
 * hosts at least one agent's latest comment.
 *
 * Deliberate scope decisions (recorded for review):
 *   - Member (human) comments never keep a root expanded. The spec says
 *     "每个智能体时间上的最后一条" — per-AGENT, not per-author. Flipping
 *     to per-author is a one-line change to `isAgentActor` below.
 *   - Resolved roots follow the same rule (no exception in the spec);
 *     forceExpanded renders the full card the same way for them.
 *   - Ordering contract: rows arrive ASC-stable from the server (see
 *     timeline-list.tsx pipeline). `created_at` ties break by later
 *     input position.
 *
 * The policy only ever ADDS expansions on top of the collapsed default
 * (timeline-list applies each returned id via the focus store's
 * expandRoot) — it never collapses anything the user expanded by hand.
 */
import type { TimelineEntry } from "@multica/core/types";
import type { TimelineRow } from "./timeline-thread";

function isAgentActor(entry: TimelineEntry): boolean {
  return entry.actor_type === "agent";
}

export function computeReentryExpandedRoots(
  rows: readonly TimelineRow[],
): Set<string> {
  // Agent actor key → the latest comment seen so far, and the root that
  // hosts it. Latest = strictly newer `created_at`, or an equal one
  // later in input order (stable ASC tie-break).
  const latestByAgent = new Map<
    string,
    { createdAt: string; rootId: string }
  >();

  const consider = (entry: TimelineEntry, rootId: string) => {
    if (entry.type !== "comment" || !isAgentActor(entry)) return;
    const key = entry.actor_id;
    const cur = latestByAgent.get(key);
    if (!cur || entry.created_at >= cur.createdAt) {
      latestByAgent.set(key, { createdAt: entry.created_at, rootId });
    }
  };

  for (const row of rows) {
    if (row.entry.type !== "comment") continue;
    consider(row.entry, row.entry.id);
    for (const reply of row.replies) consider(reply, row.entry.id);
  }

  const expanded = new Set<string>();
  for (const { rootId } of latestByAgent.values()) {
    expanded.add(rootId);
  }
  return expanded;
}
