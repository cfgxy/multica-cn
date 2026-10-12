"use client";

import { Line, LineChart, XAxis, YAxis, CartesianGrid } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import { USAGE_METRIC_WINDOWS, type UsageMetricWindow } from "@multica/core/dashboard";
import type { UsageMetricSeries } from "@multica/core/types";
import { useT } from "../../i18n";
import { Segmented } from "./dashboard-shared";

/** Arithmetic mean of a list of sample values — the ranking key for which
 *  lines earn a slot on a crowded panel. */
export function meanOf(values: number[]): number {
  if (values.length === 0) return 0;
  return values.reduce((sum, v) => sum + v, 0) / values.length;
}

// How many series a panel draws before the tail is dropped. Prometheus groups
// the traffic panel by one dimension, so a workspace with dozens of models
// would otherwise render an unreadable rainbow; the top series by mean value
// are the ones the panel exists to show.
export const METRIC_SERIES_LIMIT = 8;

export const METRIC_WINDOW_OPTIONS = USAGE_METRIC_WINDOWS;

export function MetricWindowSegmented({
  value,
  onChange,
}: {
  value: UsageMetricWindow;
  onChange: (w: UsageMetricWindow) => void;
}) {
  const { t } = useT("usage");
  const labels: Record<UsageMetricWindow, string> = {
    "1h": t(($) => $.metrics.window_1h),
    "6h": t(($) => $.metrics.window_6h),
    "24h": t(($) => $.metrics.window_24h),
    "7d": t(($) => $.metrics.window_7d),
  };
  return (
    <Segmented
      label={t(($) => $.metrics.window_label)}
      value={value}
      onChange={onChange}
      options={METRIC_WINDOW_OPTIONS.map((w) => ({ label: labels[w], value: w }))}
    />
  );
}

// Chart tokens cycle chart-1..chart-5 — the same palette the stacked usage
// charts use, so these panels never introduce a colour the rest of the page
// hasn't already taught the reader.
export function metricSeriesColor(index: number): string {
  return `var(--chart-${(index % 5) + 1})`;
}

/**
 * Rank series by mean value and keep the top N, assigning each a display
 * name unique within the panel — Prometheus can return two label sets that
 * render to the same name (same model via two runtimes when grouping by
 * model alone), and a duplicate dataKey would make recharts draw one line
 * over the other. The tail is dropped rather than folded into an "Other"
 * line: folding sums non-additive gauges (memory %) and hides shape.
 */
export function topNamedSeries<T>(
  series: T[],
  nameOf: (s: T) => string,
  meanOf: (s: T) => number,
  limit = METRIC_SERIES_LIMIT,
): { series: T; name: string }[] {
  const ranked = series
    .map((s, i) => ({ s, i, mean: meanOf(s) }))
    .sort((a, b) => b.mean - a.mean || a.i - b.i);
  const used = new Set<string>();
  const out: { series: T; name: string }[] = [];
  for (const { s } of ranked) {
    if (out.length >= limit) break;
    let name = nameOf(s) || "?";
    for (let n = 2; used.has(name); n += 1) name = `${nameOf(s)} (${n})`;
    used.add(name);
    out.push({ series: s, name });
  }
  return out;
}

/**
 * Recharts wants one row per x position with one column per series; the
 * Prometheus matrix gives one array per series. Rows are merged on the
 * sample timestamp — series from one query come back with aligned stamps,
 * and stamps that only partially overlap merely produce rows with holes,
 * which `connectNulls` bridges.
 */
export type MetricRow = { t: number } & Record<string, number>;

export function mergeMetricRows(
  named: { name: string; points: UsageMetricSeries["points"] }[],
): MetricRow[] {
  const rows = new Map<number, MetricRow>();
  for (const { name, points } of named) {
    for (const point of points) {
      let row = rows.get(point.t);
      if (!row) {
        row = { t: point.t };
        rows.set(point.t, row);
      }
      row[name] = point.v;
    }
  }
  return [...rows.values()].sort((a, b) => a.t - b.t);
}

function metricTimeLabel(t: number): string {
  const d = new Date(t * 1000);
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  // Windows longer than a day need the date to disambiguate; shorter ones
  // are all within the reader's "today" mental model.
  if (Date.now() - t * 1000 > 24 * 3600 * 1000) {
    return `${d.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
  }
  return time;
}

function metricTimeTooltip(t: number): string {
  return new Date(t * 1000).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** The dashed placeholder both live panels show while their data is absent
 *  (backend error or no samples). Shares the empty-state look of the rollup
 *  charts' no-data tile. */
export function MetricPanelMessage({ text }: { text: string }) {
  return (
    <div className="flex aspect-[3/1] max-h-32 flex-col items-center justify-center gap-2 rounded-md border border-dashed bg-muted/20 p-6 text-center">
      <p className="text-caption text-muted-foreground">{text}</p>
    </div>
  );
}

/**
 * A chart of live metric series: one coloured line per named series over a
 * shared time axis. The x labels are clock times — these panels answer "what
 * is happening / what happened recently", not "which calendar day", so the
 * calendar-day axes the rollup charts use don't apply.
 */
export function MetricLineChart({
  rows,
  series,
  formatY,
  formatTooltipValue,
}: {
  rows: MetricRow[];
  series: { name: string; color: string }[];
  formatY?: (v: number) => string;
  formatTooltipValue?: (v: number) => string;
}) {
  return (
    <ChartContainer
      config={Object.fromEntries(
        series.map((s) => [s.name, { label: s.name, color: s.color }]),
      ) satisfies ChartConfig}
      className="aspect-[3/1] w-full"
    >
      <LineChart data={rows} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="t"
          type="number"
          scale="time"
          domain={["dataMin", "dataMax"]}
          tickFormatter={metricTimeLabel}
          tickLine={false}
          axisLine={false}
          tickMargin={8}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          width="auto"
          tickFormatter={formatY ? (v: number) => formatY(v) : undefined}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(t) => metricTimeTooltip(t as number)}
              formatter={(value, name) =>
                typeof value === "number"
                  ? `${formatTooltipValue ? formatTooltipValue(value) : String(value)} ${name}`
                  : `${value} ${name}`
              }
            />
          }
        />
        {series.map((s) => (
          <Line
            key={s.name}
            dataKey={s.name}
            stroke={s.color}
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
            connectNulls
          />
        ))}
      </LineChart>
    </ChartContainer>
  );
}
