/**
 * Physical scroll-end geometry helpers for the timeline (RUYI-28).
 *
 * Extracted as pure functions so the "independent bottom chip" rules stay
 * testable: the chip depends ONLY on physical distance to the FlashList's
 * content end — never on `newCount`, the unread divider, or last-viewed
 * state (approved scope keeps those systems untouched).
 *
 * The existing AT_BOTTOM_SLACK_PX (80px, "user is about to see the bottom")
 * lives inside timeline-list.tsx and drives the new-message chip counter;
 * these helpers use a separate, tighter 48px band for a dedicated
 * "you are far from the end" affordance. Two different questions, two
 * different thresholds.
 */
export const BOTTOM_CHIP_MIN_GAP_PX = 48;

/**
 * Pixel distance from the scroll viewport's bottom edge to the physical
 * end of the content. Clamped at 0 — overscroll (bounce / negative
 * rubber-band values) must not report a negative "distance" that callers
 * would compare against a positive threshold incorrectly.
 *
 * When contentHeight < viewport the user can never scroll away from the
 * end; the clamp yields 0 via the max().
 */
export function distToPhysicalEnd(
  contentHeight: number,
  offsetY: number,
  viewportHeight: number,
): number {
  return Math.max(0, contentHeight - (offsetY + viewportHeight));
}

/**
 * The standalone "to bottom" chip is visible only when the user is
 * strictly farther than BOTTOM_CHIP_MIN_GAP_PX from the physical end.
 * Inside the band the list is effectively at the end — the chip would
 * cover content the user is already reading.
 */
export function shouldShowBottomChip(distFromEnd: number): boolean {
  return distFromEnd > BOTTOM_CHIP_MIN_GAP_PX;
}

/**
 * Last-known scroll geometry, saved by the timeline so the bottom chip can
 * be recomputed OUTSIDE scroll events — `onScroll` only fires once the user
 * actually drags, so a chip driven solely from it stays stale (or absent)
 * on mount, after data grows, and after the viewport resizes.
 */
export interface ScrollGeometry {
  contentHeight: number;
  offsetY: number;
  viewportHeight: number;
}

/**
 * Unified visibility decision over a geometry snapshot. Pure — the same
 * function answers both the scroll-event path and the mount /
 * onContentSizeChange / onLayout path, so the chip can never disagree with
 * itself between the two entry points.
 *
 * Zeroed geometry (nothing laid out yet) yields "at end": showing the chip
 * before the first layout would flash it over an empty list.
 */
export function computeBottomChipVisible(geo: ScrollGeometry): boolean {
  if (geo.viewportHeight <= 0) return false;
  return shouldShowBottomChip(
    distToPhysicalEnd(geo.contentHeight, geo.offsetY, geo.viewportHeight),
  );
}

/**
 * "To top" chip (RUYI-81) — the exact mirror of the bottom-chip block
 * above. Same 48px band, same pure/single-decision-function discipline:
 * the chip depends ONLY on physical distance from the FlashList's content
 * start (`offsetY`), never on newCount / divider / last-viewed state.
 */
export const TOP_CHIP_MIN_GAP_PX = 48;

/**
 * Pixel distance from the scroll viewport's top edge to the physical start
 * of the content — i.e. the current scroll offset. Clamped at 0: overscroll
 * bounce above the first row reports negative offsets, which must read as
 * "at top", not a phantom distance.
 */
export function distFromPhysicalTop(offsetY: number): number {
  return Math.max(0, offsetY);
}

export function shouldShowTopChip(distFromTop: number): boolean {
  return distFromTop > TOP_CHIP_MIN_GAP_PX;
}

/**
 * Mirror of {@link computeBottomChipVisible} for the content start.
 * Zeroed geometry still yields "hidden" — no layout means no trustworthy
 * offset yet.
 */
export function computeTopChipVisible(geo: ScrollGeometry): boolean {
  if (geo.viewportHeight <= 0) return false;
  return shouldShowTopChip(distFromPhysicalTop(geo.offsetY));
}

/** Slot indexes into the chip stack's bottom offsets — see
 *  `CHIP_SLOT_BOTTOM_CLASSES` in timeline-list.tsx (bottom-3 / bottom-14 /
 *  bottom-25, the 44px pitch RUYI-28 established for the first two). */
export interface ChipStackSlots {
  newChip: number;
  bottomChip: number;
  topChip: number;
}

/**
 * Pack the three floating chips into stacking slots so no two overlap,
 * whatever the visible combination. Fixed packing order new → bottom →
 * top: a lone chip always gets slot 0 (thumb-closest); each additional
 * visible chip pushes the later ones one slot up. Hidden chips get -1.
 * Pure so every combination stays regression-tested.
 */
export function assignChipStackSlots(visible: {
  newChip: boolean;
  bottomChip: boolean;
  topChip: boolean;
}): ChipStackSlots {
  let next = 0;
  const assign = (on: boolean) => (on ? next++ : -1);
  return {
    newChip: assign(visible.newChip),
    bottomChip: assign(visible.bottomChip),
    topChip: assign(visible.topChip),
  };
}
