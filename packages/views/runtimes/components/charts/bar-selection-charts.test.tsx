import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { cleanup } from "@testing-library/react";
import { renderWithI18n } from "../../../test/i18n";

// recharts cannot render in jsdom (ResponsiveContainer never measures a
// size), and the repo's convention is to stub the chart module wholesale at
// the consumer level. These tests go one layer down instead: a minimal
// recharts stub that RECORDS what each chart component passes to BarChart /
// Bar, so the selection wiring itself is under test — handlers forwarded
// when the props are present, nothing attached when they are not.

type CapturedChartProps = Record<string, unknown> & { children?: ReactNode };
const barChartProps: CapturedChartProps[] = [];
const bars: { dataKey: unknown; children: unknown }[] = [];

vi.mock("recharts", () => ({
  BarChart: ({ children, ...props }: CapturedChartProps) => {
    barChartProps.push(props);
    return <div data-testid="bar-chart-stub">{children}</div>;
  },
  Bar: ({ children, dataKey }: { children?: ReactNode; dataKey: unknown }) => {
    bars.push({ dataKey, children });
    return <div data-bar-key={String(dataKey)}>{children}</div>;
  },
  Cell: ({
    fillOpacity,
    ...rest
  }: { fillOpacity?: number } & Record<string, unknown>) => (
    <div data-cell="true" data-fill-opacity={fillOpacity} {...rest} />
  ),
  XAxis: () => null,
  YAxis: () => null,
  CartesianGrid: () => null,
  // Consumed by @multica/ui's ChartContainer.
  ResponsiveContainer: ({ children }: { children?: ReactNode }) => (
    <div>{children}</div>
  ),
  Tooltip: () => null,
  Legend: () => null,
}));

import { DailyTokensChart } from "./daily-tokens-chart";
import { DailyErrorsChart, type DailyErrorsData } from "./daily-errors-chart";
import { WeeklyTokensChart } from "./weekly-tokens-chart";
import type { BarSelection } from "./bar-selection";

const DAILY = [
  { date: "2026-10-01", label: "10/1", input: 10, output: 5, cacheRead: 2, cacheWrite: 1 },
  { date: "2026-10-02", label: "10/2", input: 12, output: 6, cacheRead: 3, cacheWrite: 2 },
  { date: "2026-10-03", label: "10/3", input: 8, output: 4, cacheRead: 1, cacheWrite: 0 },
];

function cellNodes(children: unknown): { key: string | null; fillOpacity: unknown }[] {
  if (!Array.isArray(children)) return [];
  return (children as { key: string | null; props: { fillOpacity: unknown } }[]).map(
    (n) => ({ key: n.key, fillOpacity: n.props.fillOpacity }),
  );
}

beforeEach(() => {
  barChartProps.length = 0;
  bars.length = 0;
});

describe("chart selection wiring — negative: props absent", () => {
  it("attaches no selection handlers to the chart", () => {
    renderWithI18n(<DailyTokensChart data={DAILY} />);
    expect(barChartProps).toHaveLength(1);
    expect(barChartProps[0]).not.toHaveProperty("onMouseDown");
    expect(barChartProps[0]).not.toHaveProperty("onMouseMove");
    expect(barChartProps[0]).not.toHaveProperty("onMouseUp");
    expect(barChartProps[0]).not.toHaveProperty("onMouseLeave");
    // And no highlight cells: idle bars render exactly as before.
    expect(bars.every((b) => b.children == null)).toBe(true);
  });

  it("renders a selection-capable chart inert when nothing is selected", () => {
    const onSelectionChange = vi.fn();
    renderWithI18n(
      <DailyTokensChart data={DAILY} selection={null} onSelectionChange={onSelectionChange} />,
    );
    expect(barChartProps[0]).toHaveProperty("onMouseDown");
    // Handlers attached, but with no selection there are no cells to paint.
    expect(bars.every((b) => b.children == null)).toBe(true);
  });
});

describe("chart selection wiring — positive: handlers forwarded", () => {
  it("drives the selection callback through the forwarded handlers", () => {
    const onSelectionChange = vi.fn();
    renderWithI18n(
      <DailyTokensChart data={DAILY} selection={null} onSelectionChange={onSelectionChange} />,
    );
    const props = barChartProps[0] as unknown as {
      onMouseDown: (s: unknown, e: unknown) => void;
      onMouseUp: (s: unknown, e: unknown) => void;
    };
    props.onMouseDown({ activeTooltipIndex: 1 }, { clientX: 100 });
    props.onMouseUp({ activeTooltipIndex: 1 }, { clientX: 100 });
    expect(onSelectionChange).toHaveBeenCalledWith({
      start: "2026-10-02",
      end: "2026-10-02",
    });
  });

  it("paints dim/selected cells for every bar while a selection is active", () => {
    const selection: BarSelection = { start: "2026-10-02", end: "2026-10-02" };
    renderWithI18n(
      <DailyTokensChart data={DAILY} selection={selection} onSelectionChange={vi.fn()} />,
    );
    // Four stacked series, each carrying three cells keyed by bar date.
    expect(bars).toHaveLength(4);
    for (const bar of bars) {
      const cells = cellNodes(bar.children);
      expect(cells.map((c) => c.key)).toEqual(["2026-10-01", "2026-10-02", "2026-10-03"]);
      expect(cells.map((c) => c.fillOpacity)).toEqual([0.3, 1, 0.3]);
    }
  });
});

describe("chart selection wiring — daily errors chart", () => {
  // DailyErrorsData requires every failure class to be present (0 when
  // unused), so the fixture spells the whole record out.
  const ERROR_DATA: DailyErrorsData[] = [
    {
      date: "2026-10-01",
      label: "10/1",
      auth: 1,
      rate_limit: 0,
      timeout: 2,
      provider: 0,
      runtime: 0,
      agent: 0,
      other: 0,
      failed: 3,
      total: 10,
    },
    {
      date: "2026-10-02",
      label: "10/2",
      auth: 0,
      rate_limit: 0,
      timeout: 1,
      provider: 0,
      runtime: 0,
      agent: 0,
      other: 0,
      failed: 1,
      total: 8,
    },
  ];

  it("forwards handlers and paints cells on every class series", () => {
    renderWithI18n(
      <DailyErrorsChart
        data={ERROR_DATA}
        selection={{ start: "2026-10-01", end: "2026-10-01" }}
        onSelectionChange={vi.fn()}
      />,
    );
    expect(barChartProps[0]).toHaveProperty("onMouseDown");
    expect(bars.length).toBeGreaterThan(0);
    for (const bar of bars) {
      const cells = cellNodes(bar.children);
      expect(cells.map((c) => c.fillOpacity)).toEqual([1, 0.3]);
    }
  });

  it("stays inert without selection props", () => {
    renderWithI18n(<DailyErrorsChart data={ERROR_DATA} />);
    expect(barChartProps[0]).not.toHaveProperty("onMouseDown");
    expect(bars.every((b) => b.children == null)).toBe(true);
  });
});

describe("chart selection wiring — weekly chart keeps its partial fade", () => {
  const WEEKLY = [
    {
      weekStart: "2026-09-28",
      weekEnd: "2026-10-04",
      label: "9/28",
      rangeLabel: "9/28 – 10/4",
      partial: false,
      daysCovered: 7,
      input: 10,
      output: 5,
      cacheRead: 2,
      cacheWrite: 1,
    },
    {
      weekStart: "2026-10-05",
      weekEnd: "2026-10-11",
      label: "10/5",
      rangeLabel: "10/5 – 10/11",
      partial: true,
      daysCovered: 3,
      input: 4,
      output: 2,
      cacheRead: 1,
      cacheWrite: 0,
    },
  ];

  it("merges selection dimming with the idle partial-week fade", () => {
    renderWithI18n(
      <WeeklyTokensChart
        data={WEEKLY}
        selection={{ start: "2026-10-07", end: "2026-10-07" }}
        onSelectionChange={vi.fn()}
      />,
    );
    for (const bar of bars) {
      const cells = cellNodes(bar.children);
      expect(cells.map((c) => c.fillOpacity)).toEqual([0.3, 1]);
    }
  });

  it("keeps the idle partial-week fade when no selection is active", () => {
    renderWithI18n(
      <WeeklyTokensChart data={WEEKLY} selection={null} onSelectionChange={vi.fn()} />,
    );
    // Idle rendering is the chart's own business — the selection module
    // contributes nothing, so the original partial Cells stand (full week at
    // full opacity, in-progress week at half).
    expect(bars.length).toBeGreaterThan(0);
    for (const bar of bars) {
      const cells = cellNodes(bar.children);
      expect(cells.map((c) => c.fillOpacity)).toEqual([1, 0.5]);
    }
  });

  it("maps a click to the bar's week span", () => {
    const onSelectionChange = vi.fn();
    renderWithI18n(
      <WeeklyTokensChart data={WEEKLY} selection={null} onSelectionChange={onSelectionChange} />,
    );
    const props = barChartProps[0] as unknown as {
      onMouseDown: (s: unknown, e: unknown) => void;
      onMouseUp: (s: unknown, e: unknown) => void;
    };
    props.onMouseDown({ activeTooltipIndex: 0 }, { clientX: 50 });
    props.onMouseUp({ activeTooltipIndex: 0 }, { clientX: 50 });
    expect(onSelectionChange).toHaveBeenCalledWith({
      start: "2026-09-28",
      end: "2026-10-04",
    });
  });
});

afterEach(() => {
  cleanup();
});
