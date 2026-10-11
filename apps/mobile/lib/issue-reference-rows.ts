/**
 * Pure row-building + grouping for the reference tail (RUYI-635 增量,
 * mobile half).
 *
 * The web footer builds the same union inline; mobile extracts it here
 * because RN components can't render in the node vitest lane — this module
 * carries the tail's POLICY (resolution zip, cross-form collapse, PR/degraded
 * kinds, mixed-kind grouping) where it can be unit-tested, while the
 * component (issue-reference-list.tsx) only resolves, partitions through the
 * shared `partitionReferencePreview`, and draws.
 */
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";

/** What one collected reference resolved to (positional zip with the input). */
export type ReferenceTailResolution = Issue | GitHubPullRequest | null;

export type ReferenceTailRow =
  | { kind: "issue"; key: string; issue: Issue }
  | { kind: "pr"; key: string; pr: GitHubPullRequest }
  | { kind: "degraded"; key: string; fallback: string };

export interface ReferenceTailGroups {
  /** True when issues AND PRs both resolved — only then do group headers show. */
  grouped: boolean;
  tasks: Extract<ReferenceTailRow, { kind: "issue" }>[];
  prs: Extract<ReferenceTailRow, { kind: "pr" }>[];
  /** Unresolved rows: never clickable, plain-text slots inside the sheet. */
  degraded: Extract<ReferenceTailRow, { kind: "degraded" }>[];
}

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
 */
export function buildReferenceTailRows(
  references: IssueReference[],
  resolutions: ReferenceTailResolution[],
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
      seenIssueIds.add(resolved.id);
      rows.push({ kind: "issue", key, issue: resolved });
    } else {
      rows.push({ kind: "degraded", key, fallback: token.label || token.ref });
    }
  });
  return rows;
}

/**
 * Split rows into the sheet's two groups. Group headers only render when
 * BOTH kinds resolved (Board 2: 单一类型不显示组头); a single-kind list keeps
 * first-appearance order via the tasks slice alone. Degraded rows stay out
 * of both groups — the sheet renders them as plain-text slots after.
 */
export function groupReferenceTailRows(
  rows: ReferenceTailRow[],
): ReferenceTailGroups {
  const tasks = rows.filter(
    (row): row is Extract<ReferenceTailRow, { kind: "issue" }> =>
      row.kind === "issue",
  );
  const prs = rows.filter(
    (row): row is Extract<ReferenceTailRow, { kind: "pr" }> => row.kind === "pr",
  );
  const degraded = rows.filter(
    (row): row is Extract<ReferenceTailRow, { kind: "degraded" }> =>
      row.kind === "degraded",
  );
  return { grouped: tasks.length > 0 && prs.length > 0, tasks, prs, degraded };
}
