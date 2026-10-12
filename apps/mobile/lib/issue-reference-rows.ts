/**
 * Pure row-building for the reference tail (RUYI-635 增量, mobile half).
 *
 * The web footer builds the same union inline; mobile extracts it here
 * because RN components can't render in the node vitest lane — this module
 * carries the tail's POLICY (resolution zip, cross-form collapse, RUYI-731
 * self-filter, PR/degraded kinds) where it can be unit-tested, while the
 * component (issue-reference-list.tsx) only resolves and draws.
 */
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";

/** What one collected reference resolved to (positional zip with the input). */
export type ReferenceTailResolution = Issue | GitHubPullRequest | null;

export type ReferenceTailRow =
  | { kind: "issue"; key: string; issue: Issue }
  | { kind: "pr"; key: string; pr: GitHubPullRequest }
  | { kind: "degraded"; key: string; fallback: string };

function isPullRequest(value: ReferenceTailResolution): value is GitHubPullRequest {
  return value !== null && "html_url" in value;
}

/**
 * Zip the collected references with their positional resolutions into the
 * rendered row union. Cross-form collapse works on RESOLVED identity: the
 * same issue reached via a UUID mention and a bare identifier is one row, at
 * its first mention's position; two tokens resolving to the same PR collapse
 * the same way. A null resolution keeps its slot as a degraded row — the
 * "miss reads as what the author typed" contract.
 *
 * RUYI-731 self-filter: when `currentIssueId` is set (issue-detail context),
 * a reference resolving to the CURRENT issue builds no row — an issue
 * describing itself is not a reference. Surfaces without the context pass
 * null and keep every row.
 */
export function buildReferenceTailRows(
  references: IssueReference[],
  resolutions: ReferenceTailResolution[],
  currentIssueId: string | null = null,
): ReferenceTailRow[] {
  const rows: ReferenceTailRow[] = [];
  const seenIssueIds = new Set<string>();
  const seenPrIds = new Set<string>();
  references.forEach((token, i) => {
    const key = `${token.form}:${token.ref}`;
    const resolved = resolutions[i] ?? null;
    if (isPullRequest(resolved)) {
      if (seenPrIds.has(resolved.id)) return;
      seenPrIds.add(resolved.id);
      rows.push({ kind: "pr", key, pr: resolved });
    } else if (resolved) {
      if (seenIssueIds.has(resolved.id)) return;
      if (resolved.id === currentIssueId) return;
      seenIssueIds.add(resolved.id);
      rows.push({ kind: "issue", key, issue: resolved });
    } else {
      rows.push({ kind: "degraded", key, fallback: token.label || token.ref });
    }
  });
  return rows;
}
