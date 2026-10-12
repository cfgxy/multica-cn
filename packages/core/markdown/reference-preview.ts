/**
 * Preview/fold partition for the reference tail (RUYI-635 增量, Owner point
 * ①④): the inline block never grows past `maxPreview` rows — everything past
 * that collapses into one "N more references (incl. M PRs)" line and the full
 * list lives in the tap/popover surface. Web and mobile render different
 * glyphs but MUST fold at the same cut with the same counts, so the partition
 * lives here next to the extractor both ends already share (the 2026-05-09
 * "both ends compute, neither trusts" rule, applied to the fold).
 *
 * Pure and generic: callers hand their own row objects and a `kindOf` reader
 * ("issue" | "pr" | "degraded"); order is preserved in both slices.
 */
export type ReferenceRowKind = "issue" | "pr" | "degraded";

export interface ReferencePreviewPartition<T> {
  /** The inline rows to render (at most `maxPreview`). */
  previews: T[];
  /** Rows replaced by the collapse line, in original order. */
  hidden: T[];
  /** PR rows inside `hidden` — the "incl. M PRs" count. */
  hiddenPrCount: number;
}

export function partitionReferencePreview<T>(
  rows: T[],
  kindOf: (row: T) => ReferenceRowKind,
  maxPreview = 2,
): ReferencePreviewPartition<T> {
  const previews = rows.slice(0, maxPreview);
  const hidden = rows.slice(maxPreview);
  const hiddenPrCount = hidden.filter((row) => kindOf(row) === "pr").length;
  return { previews, hidden, hiddenPrCount };
}
