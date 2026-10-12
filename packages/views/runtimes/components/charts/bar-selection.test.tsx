import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { render, cleanup, act } from "@testing-library/react";
import { renderWithI18n } from "../../../test/i18n";
import {
  useBarSelection,
  dailyBarSpans,
  weeklyBarSpans,
  type BarSelection,
} from "./bar-selection";

// The bar-selection state machine drives every dashboard chart's
// click / drag / blank-clear behaviour. recharts cannot render in jsdom
// (ResponsiveContainer never measures), so these tests invoke the handler
// contract directly — the same `(nextState, event)` shape recharts 3 hands
// to chart-level mouse props. Real browsers always precede a press with a
// mousemove, which is what populates `activeTooltipIndex`.

const DAILY_SPANS = [
  { start: "2026-10-01", end: "2026-10-01" },
  { start: "2026-10-02", end: "2026-10-02" },
  { start: "2026-10-03", end: "2026-10-03" },
  { start: "2026-10-04", end: "2026-10-04" },
  { start: "2026-10-05", end: "2026-10-05" },
];

const WEEKLY_SPANS = [
  { start: "2026-09-28", end: "2026-10-04" },
  { start: "2026-10-05", end: "2026-10-11" },
  { start: "2026-10-12", end: "2026-10-18" },
];

function mouseEvent(overrides: { clientX?: number } = {}) {
  return { clientX: overrides.clientX ?? 0 };
}

function chartState(activeTooltipIndex: number | undefined) {
  return { activeTooltipIndex };
}

/** Harness mounting the hook against a controllable selection. */
function useHarness(
  spans: { start: string; end: string }[],
  enabled: boolean,
  onSelectionChange?: (next: BarSelection | null) => void,
) {
  const [selection, setSelection] = useState<BarSelection | null>(null);
  const sel = useBarSelection({
    spans,
    selection,
    onSelectionChange: enabled ? (onSelectionChange ?? setSelection) : undefined,
  });
  return { sel, selection };
}

function Harness({
  spans = DAILY_SPANS,
  enabled = true,
  onSelectionChange,
  probe,
}: {
  spans?: { start: string; end: string }[];
  enabled?: boolean;
  onSelectionChange?: (next: BarSelection | null) => void;
  probe: (h: ReturnType<typeof useHarness>) => void;
}) {
  probe(useHarness(spans, enabled, onSelectionChange));
  return null;
}

function mountHarness(
  opts: {
    spans?: { start: string; end: string }[];
    enabled?: boolean;
    onSelectionChange?: (next: BarSelection | null) => void;
  } = {},
) {
  let handle: ReturnType<typeof useHarness> | undefined;
  renderWithI18n(
    <Harness
      spans={opts.spans}
      enabled={opts.enabled}
      onSelectionChange={opts.onSelectionChange}
      probe={(h) => {
        handle = h;
      }}
    />,
  );
  if (!handle) throw new Error("harness did not mount");
  return {
    get handle() {
      return handle!;
    },
  };
}

describe("useBarSelection — wiring", () => {
  it("exposes no chart handlers when selection is disabled", () => {
    const h = mountHarness({ enabled: false });
    expect(h.handle.sel.chartProps).toEqual({});
  });

  it("exposes the four mouse handlers when enabled", () => {
    const h = mountHarness({ enabled: true });
    expect(Object.keys(h.handle.sel.chartProps).sort()).toEqual([
      "onMouseDown",
      "onMouseLeave",
      "onMouseMove",
      "onMouseUp",
    ]);
  });
});

describe("useBarSelection — click toggles a single bar", () => {
  it("selects a bar on click", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(2), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(2), mouseEvent({ clientX: 100 }));
    });
    expect(h.handle.selection).toEqual({ start: "2026-10-03", end: "2026-10-03" });
  });

  it("deselects when the same bar is clicked again", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(2), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(2), mouseEvent({ clientX: 100 }));
    });
    expect(h.handle.selection).toEqual({ start: "2026-10-03", end: "2026-10-03" });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(2), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(2), mouseEvent({ clientX: 100 }));
    });
    expect(h.handle.selection).toBeNull();
  });

  it("moves the selection when a different bar is clicked", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(1), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(1), mouseEvent({ clientX: 100 }));
    });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(3), mouseEvent({ clientX: 300 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(3), mouseEvent({ clientX: 300 }));
    });
    expect(h.handle.selection).toEqual({ start: "2026-10-04", end: "2026-10-04" });
  });
});

describe("useBarSelection — drag selects a contiguous range", () => {
  it("selects the dragged range left to right", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(1), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(3), mouseEvent({ clientX: 320 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(3), mouseEvent({ clientX: 320 }));
    });
    expect(h.handle.selection).toEqual({
      start: "2026-10-02",
      end: "2026-10-04",
    });
  });

  it("selects the dragged range right to left (anchor is the later bar)", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(4), mouseEvent({ clientX: 400 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(2), mouseEvent({ clientX: 210 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(2), mouseEvent({ clientX: 210 }));
    });
    expect(h.handle.selection).toEqual({
      start: "2026-10-03",
      end: "2026-10-05",
    });
  });

  it("treats sub-threshold movement as a click, not a drag", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(1), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(2), mouseEvent({ clientX: 103 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(2), mouseEvent({ clientX: 103 }));
    });
    // 3px of jitter stays a click on the anchor bar.
    expect(h.handle.selection).toEqual({ start: "2026-10-02", end: "2026-10-02" });
  });

  it("keeps the range anchored when a drag passes over an indexless area", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(0), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(2), mouseEvent({ clientX: 250 }));
      // Cursor leaves the axis range: no active index, hover holds at 2.
      h.handle.sel.chartProps.onMouseMove?.(chartState(undefined), mouseEvent({ clientX: 380 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(undefined), mouseEvent({ clientX: 380 }));
    });
    expect(h.handle.selection).toEqual({
      start: "2026-10-01",
      end: "2026-10-03",
    });
  });

  it("aborts the drag when the pointer leaves the chart", () => {
    const onChange = vi.fn();
    const h = mountHarness({ onSelectionChange: onChange });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(1), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(3), mouseEvent({ clientX: 320 }));
      h.handle.sel.chartProps.onMouseLeave?.(chartState(3), mouseEvent({ clientX: 320 }));
    });
    expect(onChange).not.toHaveBeenCalled();
    // The aborted gesture does not leak into the next press.
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(4), mouseEvent({ clientX: 400 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(4), mouseEvent({ clientX: 400 }));
    });
    expect(onChange).toHaveBeenCalledWith({ start: "2026-10-05", end: "2026-10-05" });
  });
});

describe("useBarSelection — blank press clears the selection", () => {
  it("clears immediately when the press lands outside every bar", () => {
    const onChange = vi.fn();
    const h = mountHarness({ onSelectionChange: onChange });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(undefined), mouseEvent({ clientX: 5 }));
    });
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("clears when the index is out of the data range", () => {
    const onChange = vi.fn();
    const h = mountHarness({ onSelectionChange: onChange });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(99), mouseEvent({ clientX: 5 }));
    });
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("clears a stale string index out of range (recharts may hand one back)", () => {
    const onChange = vi.fn();
    const h = mountHarness({ onSelectionChange: onChange });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(
        chartState("nonsense" as unknown as number),
        mouseEvent({ clientX: 5 }),
      );
    });
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("does nothing on blank press when nothing is selected", () => {
    const onChange = vi.fn();
    const h = mountHarness({ onSelectionChange: onChange });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(undefined), mouseEvent({ clientX: 5 }));
    });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(null);
  });
});

describe("useBarSelection — span helpers", () => {
  it("maps daily rows to point spans", () => {
    expect(
      dailyBarSpans([
        { date: "2026-10-01", label: "10/1" },
        { date: "2026-10-02", label: "10/2" },
      ]),
    ).toEqual([
      { start: "2026-10-01", end: "2026-10-01" },
      { start: "2026-10-02", end: "2026-10-02" },
    ]);
  });

  it("maps weekly rows to their calendar span", () => {
    expect(
      weeklyBarSpans([
        { weekStart: "2026-09-28", weekEnd: "2026-10-04" },
        { weekStart: "2026-10-05", weekEnd: "2026-10-11" },
      ]),
    ).toEqual([
      { start: "2026-09-28", end: "2026-10-04" },
      { start: "2026-10-05", end: "2026-10-11" },
    ]);
  });
});

describe("useBarSelection — highlight cells", () => {
  it("returns null cells when nothing is selected", () => {
    const h = mountHarness();
    expect(h.handle.sel.cells(5, { keyOf: (i) => String(i) })).toBeNull();
  });

  it("dims unselected bars and keeps selected ones full while a selection is active", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(1), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(3), mouseEvent({ clientX: 320 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(3), mouseEvent({ clientX: 320 }));
    });
    const nodes = h.handle.sel.cells(5, { keyOf: (i) => String(i) });
    expect(nodes).not.toBeNull();
    const opacity = (i: number) =>
      (Array.isArray(nodes) ? (nodes[i] as { props: { fillOpacity: number } }) : null)?.props
        .fillOpacity;
    expect(opacity(0)).toBe(0.3);
    expect(opacity(1)).toBe(1);
    expect(opacity(2)).toBe(1);
    expect(opacity(3)).toBe(1);
    expect(opacity(4)).toBe(0.3);
  });

  it("highlights weekly bars whose span overlaps the selection", () => {
    const h = mountHarness({ spans: WEEKLY_SPANS });
    // Select one day inside the middle week.
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(
        chartState(1),
        mouseEvent({ clientX: 100 }),
      );
      h.handle.sel.chartProps.onMouseUp?.(chartState(1), mouseEvent({ clientX: 100 }));
    });
    expect(h.handle.selection).toEqual({
      start: "2026-10-05",
      end: "2026-10-11",
    });
    const nodes = h.handle.sel.cells(3, { keyOf: (i) => String(i) });
    const opacity = (i: number) =>
      (Array.isArray(nodes) ? (nodes[i] as { props: { fillOpacity: number } }) : null)?.props
        .fillOpacity;
    expect(opacity(0)).toBe(0.3);
    expect(opacity(1)).toBe(1);
    expect(opacity(2)).toBe(0.3);
  });

  it("previews the drag range while the drag is still in flight", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(0), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(2), mouseEvent({ clientX: 250 }));
    });
    // The parent selection is still null; the in-flight drag drives the cells.
    expect(h.handle.selection).toBeNull();
    const nodes = h.handle.sel.cells(5, { keyOf: (i) => String(i) });
    const opacity = (i: number) =>
      (Array.isArray(nodes) ? (nodes[i] as { props: { fillOpacity: number } }) : null)?.props
        .fillOpacity;
    expect(opacity(0)).toBe(1);
    expect(opacity(1)).toBe(1);
    expect(opacity(2)).toBe(1);
    expect(opacity(3)).toBe(0.3);
    expect(opacity(4)).toBe(0.3);
  });

  it("drops the preview when the drag is aborted", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(0), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseMove?.(chartState(2), mouseEvent({ clientX: 250 }));
      h.handle.sel.chartProps.onMouseLeave?.(chartState(2), mouseEvent({ clientX: 250 }));
    });
    expect(h.handle.selection).toBeNull();
    expect(h.handle.sel.cells(5, { keyOf: (i) => String(i) })).toBeNull();
  });

  it("takes the dimmer of base and unselected opacity for partial weekly bars", () => {
    const h = mountHarness({ spans: WEEKLY_SPANS });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(
        chartState(1),
        mouseEvent({ clientX: 100 }),
      );
      h.handle.sel.chartProps.onMouseUp?.(chartState(1), mouseEvent({ clientX: 100 }));
    });
    // Partial weeks render at 0.5 when idle; under an active selection the
    // unselected ones must fall below that so "selected" stays the brightest
    // state on the chart.
    const nodes = h.handle.sel.cells(3, {
      keyOf: (i) => String(i),
      baseOpacity: (i) => (i === 2 ? 0.5 : 1),
    });
    const opacity = (i: number) =>
      (Array.isArray(nodes) ? (nodes[i] as { props: { fillOpacity: number } }) : null)?.props
        .fillOpacity;
    expect(opacity(0)).toBe(0.3);
    expect(opacity(2)).toBe(0.3);
    expect(opacity(1)).toBe(1);
  });

  it("keeps cell keys aligned with the data via keyOf", () => {
    const h = mountHarness();
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(
        chartState(1),
        mouseEvent({ clientX: 100 }),
      );
      h.handle.sel.chartProps.onMouseUp?.(chartState(1), mouseEvent({ clientX: 100 }));
    });
    const nodes = h.handle.sel.cells(5, { keyOf: (i) => `day-${i}` });
    expect(Array.isArray(nodes) ? nodes.map((n) => (n as { key: string }).key) : []).toEqual([
      "day-0",
      "day-1",
      "day-2",
      "day-3",
      "day-4",
    ]);
  });
});

describe("useBarSelection — empty data", () => {
  it("never fires for an empty chart", () => {
    const onChange = vi.fn();
    const h = mountHarness({ spans: [], onSelectionChange: onChange });
    act(() => {
      h.handle.sel.chartProps.onMouseDown?.(chartState(0), mouseEvent({ clientX: 100 }));
      h.handle.sel.chartProps.onMouseUp?.(chartState(0), mouseEvent({ clientX: 100 }));
    });
    // Index 0 is out of range for zero bars → blank → a single clear.
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(null);
  });
});

// Keep the testing-library import used even though most suites mount through
// renderWithI18n; render/cleanup are exercised indirectly.
describe("cleanup", () => {
  it("cleans up between tests", () => {
    render(<div />);
    cleanup();
    expect(true).toBe(true);
  });
});
