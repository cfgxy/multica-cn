/**
 * Pure row-building + folding + grouping for the reference tail (RUYI-635
 * 增量, mobile half).
 *
 * The web footer builds the same union inline; mobile extracts it here
 * because RN components can't render in the node vitest lane — this module
 * carries the tail's POLICY (resolution zip, cross-form collapse, RUYI-731
 * self-filter, preview fold, sheet grouping, drag-to-close threshold, PR/
 * degraded kinds) where it can be unit-tested, while the component
 * (issue-reference-list.tsx) only resolves and draws.
 */
import type { IssueReference } from "@multica/core/markdown";
import type { GitHubPullRequest, Issue } from "@multica/core/types";

/** What one collected reference resolved to (positional zip with the input). */
export type ReferenceTailResolution = Issue | GitHubPullRequest | null;

export type ReferenceTailRow =
  | { kind: "issue"; key: string; issue: Issue }
  | { kind: "pr"; key: string; pr: GitHubPullRequest }
  | { kind: "degraded"; key: string; fallback: string };

export type ReferenceRowKind = ReferenceTailRow["kind"];

export interface ReferencePreviewPartition {
  /** The inline rows to render (at most `maxPreview`). */
  previews: ReferenceTailRow[];
  /** Rows replaced by the collapse line, in original order. */
  hidden: ReferenceTailRow[];
  /** PR rows inside `hidden` — the "incl. M PRs" count. */
  hiddenPrCount: number;
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
 *
 * RUYI-731 self-filter: when `currentIssueId` is set (issue-detail context),
 * a reference resolving to the CURRENT issue builds no row — an issue
 * describing itself is not a reference. Surfaces without the context pass
 * null and keep every row. The filter runs BEFORE the preview fold, so the
 * self row consumes no preview slot and never appears in the sheet.
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

/**
 * Preview/fold partition for the reference tail. RUYI-731 (Owner correction
 * 2026-10-12) scoped "flat, no fold" to the web footer only — mobile keeps
 * the two-level anti-mistouch surface, so the fold arithmetic lives here.
 * The old shared core helper (partitionReferencePreview in
 * `@multica/core/markdown`) was retired when web went flat: its only
 * remaining consumer is mobile, and the "both ends fold at the same cut"
 * parity rule it served no longer holds — mobile-local keeps the policy next
 * to the row building it operates on, node-testable as before.
 *
 * The inline block never grows past `maxPreview` rows — everything past
 * that collapses into one "N more references (incl. M PRs)" line and the
 * full list lives in the bottom sheet. Pure; order is preserved in both
 * slices.
 */
export function partitionReferencePreview(
  rows: ReferenceTailRow[],
  maxPreview = 2,
): ReferencePreviewPartition {
  const previews = rows.slice(0, maxPreview);
  const hidden = rows.slice(maxPreview);
  const hiddenPrCount = hidden.filter((row) => row.kind === "pr").length;
  return { previews, hidden, hiddenPrCount };
}

/**
 * Split rows into the sheet's two groups. Group headers only render when
 * BOTH kinds resolved (Board 2: 单一类型不显示组头); a single-kind list keeps
 * first-appearance order via the tasks slice alone. Degraded rows stay out
 * of both groups — the sheet renders them as plain-text slots after.
 */
export function groupReferenceTailRows(
  rows: ReferenceTailRow[],
): {
  grouped: boolean;
  tasks: Extract<ReferenceTailRow, { kind: "issue" }>[];
  prs: Extract<ReferenceTailRow, { kind: "pr" }>[];
  /** Unresolved rows: never clickable, plain-text slots inside the sheet. */
  degraded: Extract<ReferenceTailRow, { kind: "degraded" }>[];
} {
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

// Board 2 close path #3 (下滑手势): a drag on the sheet header closes the
// sheet when the release point travels 72px+ downward OR flings down at
// ≥0.5 px/ms (~500 px/s). Upward drags never close. Thresholds live here so
// the policy stays node-testable; the component only wires the PanResponder.
export function shouldCloseSheetOnDragRelease(dy: number, vy: number): boolean {
  return dy > 72 || vy > 0.5;
}
