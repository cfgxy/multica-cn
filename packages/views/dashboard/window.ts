import { addDaysIso } from "../runtimes/utils";

// ---------------------------------------------------------------------------
// Dashboard time windows
//
// A stat window is TWO things the old UI fused into one control: how long a
// window is (1d / 7d / 30d …) and where it sits on the calendar. The quick
// ranges always sat at "ending today"; this module adds the second axis so
// the dashboard can show yesterday, last month, or any past span. Everything
// on this page — all six queries, every KPI, every chart — derives from one
// `StatWindow`, which is what keeps every card on the same time boundary.
//
// All arithmetic runs on YYYY-MM-DD strings in the viewer's timezone, the
// same axis the backend slices `bucket_hour` on (see runtimes/utils.ts —
// pure string math, so DST transitions never shift a boundary by an hour).
// ---------------------------------------------------------------------------

// Period selector — mirrors the runtime detail page so users see the same
// option set across both dashboards. `dims` declares which chart dimensions
// each range may be drawn at: 1d / 7d at the weekly grain collapse to a single
// bar, 180d at the daily grain is 180 unreadable bars.
//
// 1d semantic: "today" (the natural calendar day from 00:00 in the viewer's
// timezone), not "the last 24 hours". The window trim on the page enforces
// this even at the midnight edge.
export const TIME_RANGES = [
  { label: "1d", days: 1, dims: ["daily"] as const },
  { label: "7d", days: 7, dims: ["daily"] as const },
  { label: "30d", days: 30, dims: ["daily", "weekly"] as const },
  { label: "90d", days: 90, dims: ["daily", "weekly"] as const },
  { label: "180d", days: 180, dims: ["weekly"] as const },
] as const;

export type TimeRange = (typeof TIME_RANGES)[number]["days"];
export type Dim = "daily" | "weekly";

/** Inclusive calendar window in the viewer's timezone — YYYY-MM-DD strings. */
export interface StatWindow {
  start: string;
  end: string;
}

/** The current window for a quick range: its `days` natural days ending
 *  today (viewer tz). Offset zero of the period navigation. */
export function quickWindow(days: TimeRange, today: string): StatWindow {
  return shiftedWindow(days, 0, today);
}

/** The window `offset` whole periods back from the current one — one period
 *  is the full range length, so 1d steps a day at a time while 30d slides
 *  in whole 30-day blocks. */
export function shiftedWindow(
  days: TimeRange,
  offset: number,
  today: string,
): StatWindow {
  const end = addDaysIso(today, -offset * days);
  return { start: addDaysIso(end, -(days - 1)), end };
}

/** Inclusive day count of a window. UTC math so month/year/DST edges stay
 *  exact (same convention as addDaysIso). */
export function windowLength(w: StatWindow): number {
  const [sy, sm, sd] = w.start.split("-").map(Number);
  const [ey, em, ed] = w.end.split("-").map(Number);
  const a = Date.UTC(sy ?? 1970, (sm ?? 1) - 1, sd ?? 1);
  const b = Date.UTC(ey ?? 1970, (em ?? 1) - 1, ed ?? 1);
  return Math.round((b - a) / 86_400_000) + 1;
}

/** Move a window by `periods` whole windows of its own length — the step
 *  custom (date-picked) windows navigate with. */
export function shiftWindow(w: StatWindow, periods: number): StatWindow {
  const step = periods * windowLength(w);
  return { start: addDaysIso(w.start, step), end: addDaysIso(w.end, step) };
}

/** A window may move forward one period only when the moved window still
 *  ends today or earlier — there is no data in the future, so "next" dies
 *  at the present. */
export function canShiftNext(w: StatWindow, today: string): boolean {
  return addDaysIso(w.end, windowLength(w)) <= today;
}

/** The window sits at "now" when it ends today — the condition the
 *  back-to-current affordance shows on. */
export function isCurrentWindow(w: StatWindow, today: string): boolean {
  return w.end === today;
}

/**
 * Which chart dimensions the current range may be drawn at.
 *
 * The constraint between range and dimension used to be enforced in both
 * directions by two sibling controls in the page header: picking Weekly while
 * on 1d silently reset the range to 90d, which moved every KPI on the page.
 * The range is now the page-scoped filter and the dimension is card-scoped, so
 * the dependency runs one way only — a card offers whichever dimensions its
 * range allows, and nothing resets. A card-scoped control must never reach up
 * and change a page-scoped one (MUL-5759).
 *
 * Custom windows ride the same ladder as the quick ranges: a window of the
 * same length offers the same dimensions, so a date-picked 30-day span and
 * the 30d quick range chart identically.
 */
export function dimsForWindowLength(length: number): readonly Dim[] {
  const range =
    TIME_RANGES.find((r) => r.days >= length) ??
    TIME_RANGES[TIME_RANGES.length - 1];
  return range?.dims ?? (["daily"] as const);
}

export function dimsForDays(days: TimeRange): readonly Dim[] {
  return dimsForWindowLength(days);
}
