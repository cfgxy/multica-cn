import { useCallback, useRef, useState, type ReactNode } from "react";
import { Cell } from "recharts";

// ---------------------------------------------------------------------------
// Bar selection for the dashboard charts.
//
// Bars on the analytics page are a time axis, so a selection is a RANGE OF
// DATES, not a set of bar indices: clicking a daily bar selects that day,
// clicking a weekly bar selects its week, dragging across bars selects the
// span they cover, and a press on blank chart ground clears everything. The
// page owns the selected range — the leaderboard re-queries its per-agent
// rollups for exactly those dates — so this module only turns pointer
// gestures into `{ start, end }` spans and paints the highlight.
//
// The gesture state rides recharts 3's chart-level mouse handlers, whose
// `(nextState, event)` payload carries `activeTooltipIndex` from the current
// hover state. A real press is always preceded by a mousemove (that is what
// made the tooltip follow the cursor), so the index is populated by the time
// it matters; without one — programmatic clicks, presses on margins — the
// index is undefined and the press counts as blank. Bars are identified by
// index into the chart's data array, which the callers map to date spans via
// `dailyBarSpans` / `weeklyBarSpans`.
// ---------------------------------------------------------------------------

/** Inclusive ISO date span — a selection, or one bar's coverage. */
export interface BarSelection {
  start: string;
  end: string;
}

/** One bar's inclusive date coverage, index-aligned with the chart data. */
export type BarSpan = BarSelection;

/** The slice of recharts' chart mouse state this module reads. */
interface ChartMouseState {
  // recharts' own MouseHandlerDataParam types this as `number | string | null
  // | undefined`; the null stays in the type so our handlers remain assignable
  // to recharts' CategoricalChartFunc.
  activeTooltipIndex?: number | string | null | undefined;
}

interface ReactMouseEventLike {
  clientX: number;
}

type ChartMouseHandler = (
  state: ChartMouseState | null | undefined,
  event: ReactMouseEventLike,
) => void;

/** The chart-level mouse props this module installs when enabled. */
interface ChartMouseProps {
  onMouseDown: ChartMouseHandler;
  onMouseMove: ChartMouseHandler;
  onMouseUp: ChartMouseHandler;
  // recharts types onMouseLeave with the same two-arg CategoricalChartFunc as
  // the other handlers; the implementation just ignores both arguments.
  onMouseLeave: ChartMouseHandler;
}

export interface BarSelectionInput {
  /** Per-bar date spans, index-aligned with the chart's data array. */
  spans: readonly BarSpan[];
  /** Currently selected range, or null. */
  selection?: BarSelection | null;
  /** Called with the new range, or null when cleared. Omit to disable. */
  onSelectionChange?: ((next: BarSelection | null) => void) | undefined;
}

export interface BarSelectionController {
  /** True when selection handlers are attached. */
  enabled: boolean;
  /** Spread onto the recharts chart element. Empty when disabled. */
  chartProps: ChartMouseProps | Record<string, never>;
  /**
   * Highlight cells for a stacked/plain `<Bar>`: dims every bar outside the
   * effective range (in-flight drag preview first, committed selection
   * otherwise) and returns null when no range is active, so idle charts
   * render exactly as before.
   */
  cells: (
    count: number,
    opts?: {
      keyOf?: (index: number) => string;
      /** Idle-state opacity the bar would render with (partial weeks: 0.5). */
      baseOpacity?: (index: number) => number;
    },
  ) => ReactNode | null;
}

// Pointer jitter under this many pixels between press and release is a click,
// not the start of a drag.
const DRAG_THRESHOLD_PX = 6;

// Unselected bars sink to this opacity while a selection is active; selected
// bars stay at 1 so "selected" is always the brightest state on the chart.
const DIMMED_OPACITY = 0.3;

function normalizeIndex(
  state: ChartMouseState | null | undefined,
  length: number,
): number | null {
  const raw = state?.activeTooltipIndex;
  if (raw === undefined || raw === null) return null;
  const n = typeof raw === "number" ? raw : Number(raw);
  return Number.isInteger(n) && n >= 0 && n < length ? n : null;
}

function spansEqual(
  a: { start: string; end: string },
  b: { start: string; end: string },
): boolean {
  return a.start === b.start && a.end === b.end;
}

function rangeFor(a: number, b: number, spans: readonly BarSpan[]): BarSelection {
  const [lo, hi] = a <= b ? [a, b] : [b, a];
  return { start: spans[lo]!.start, end: spans[hi]!.end };
}

/** Map daily-grain rows (one `date` per bar) to point spans. */
export function dailyBarSpans<T extends { date: string }>(
  data: readonly T[],
): BarSpan[] {
  return data.map((d) => ({ start: d.date, end: d.date }));
}

/** Map weekly-grain rows to their calendar week span. */
export function weeklyBarSpans<T extends { weekStart: string; weekEnd: string }>(
  data: readonly T[],
): BarSpan[] {
  return data.map((w) => ({ start: w.weekStart, end: w.weekEnd }));
}

export function useBarSelection({
  spans,
  selection,
  onSelectionChange,
}: BarSelectionInput): BarSelectionController {
  // The anchor index of the in-flight press, null when the pointer is up or
  // the press landed on blank ground. Everything else about the gesture is
  // small enough to live beside it.
  const anchorRef = useRef<number | null>(null);
  const dragStartXRef = useRef(0);
  const draggedRef = useRef(false);
  const hoverRef = useRef<number | null>(null);
  // Live preview while a drag is in flight, so the highlight follows the
  // pointer before the parent commits the range.
  const [dragPreview, setDragPreview] = useState<BarSelection | null>(null);

  const enabled = onSelectionChange !== undefined;

  const resetGesture = useCallback(() => {
    anchorRef.current = null;
    draggedRef.current = false;
    hoverRef.current = null;
    setDragPreview(null);
  }, []);

  const onMouseDown = useCallback(
    (state: ChartMouseState | null | undefined, event: ReactMouseEventLike) => {
      if (!enabled) return;
      const idx = normalizeIndex(state, spans.length);
      if (idx === null) {
        // Blank ground: margins, axes, or beyond the axis range. A press here
        // is a clear — dragging from nowhere has no meaning.
        onSelectionChange?.(null);
        resetGesture();
        return;
      }
      anchorRef.current = idx;
      dragStartXRef.current = event.clientX;
      draggedRef.current = false;
      hoverRef.current = idx;
    },
    [enabled, spans, onSelectionChange, resetGesture],
  );

  const onMouseMove = useCallback(
    (state: ChartMouseState | null | undefined, event: ReactMouseEventLike) => {
      const anchor = anchorRef.current;
      if (!enabled || anchor === null) return;
      if (
        !draggedRef.current &&
        Math.abs(event.clientX - dragStartXRef.current) > DRAG_THRESHOLD_PX
      ) {
        draggedRef.current = true;
      }
      if (!draggedRef.current) return;
      const idx = normalizeIndex(state, spans.length);
      if (idx !== null) hoverRef.current = idx;
      const hover = hoverRef.current;
      if (hover !== null) setDragPreview(rangeFor(anchor, hover, spans));
    },
    [enabled, spans],
  );

  const onMouseUp = useCallback(
    (_state: ChartMouseState | null | undefined, _event: ReactMouseEventLike) => {
      const anchor = anchorRef.current;
      if (!enabled || anchor === null) return;
      if (!draggedRef.current) {
        // A click lands on the bar the press started on — the anchor, not
        // wherever the cursor drifted by release time.
        const span = spans[anchor]!;
        // Same bar again → deselect; anything else → (re)select that bar.
        onSelectionChange?.(selection && spansEqual(selection, span) ? null : span);
      } else {
        const hover = hoverRef.current;
        if (hover !== null) onSelectionChange?.(rangeFor(anchor, hover, spans));
        // Released over an indexless area: the gesture is cancelled, the
        // committed selection stands.
      }
      resetGesture();
    },
    [enabled, spans, selection, onSelectionChange, resetGesture],
  );

  const onMouseLeave = useCallback(() => {
    if (!enabled) return;
    // Leaving mid-press aborts the gesture; the release outside the chart
    // would otherwise leave the drag latched.
    resetGesture();
  }, [enabled, resetGesture]);

  const cells = useCallback<BarSelectionController["cells"]>(
    (count, opts) => {
      const effective = dragPreview ?? selection;
      if (!enabled || !effective) return null;
      return Array.from({ length: count }, (_, i) => {
        const span = spans[i];
        const selected =
          span !== undefined &&
          span.start <= effective.end &&
          span.end >= effective.start;
        const base = opts?.baseOpacity?.(i) ?? 1;
        return (
          <Cell
            key={opts?.keyOf?.(i) ?? i}
            fillOpacity={selected ? 1 : Math.min(DIMMED_OPACITY, base)}
          />
        );
      });
    },
    [enabled, spans, selection, dragPreview],
  );

  return {
    enabled,
    chartProps: enabled ? { onMouseDown, onMouseMove, onMouseUp, onMouseLeave } : {},
    cells,
  };
}
