"use client";

// RUYI-859 stage 1 — the Runtime detail page's host resources card: the
// workspace's system-resource panel (RUYI-618) narrowed server-side to the
// one daemon host this runtime binds. Unlike the dashboard panel, the three
// structured empties are VISIBLE states, not silent hiding — a deployment
// without Prometheus, a runtime that binds no daemon host, and a host that
// has not produced samples each get their own tile so the reader always
// knows why the panel is (or isn't) full.

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, RefreshCw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  formatDuration,
  type UsageMetricWindow,
} from "@multica/core/dashboard";
import type { AgentRuntime, UsageMetricSeries } from "@multica/core/types";
import { runtimeResourcesOptions } from "@multica/core/runtimes/queries";
import { useT, useTimeAgo } from "../../i18n";
import {
  MetricLineChart,
  MetricWindowSegmented,
  mergeMetricRows,
  metricSeriesColor,
  topNamedSeries,
} from "../../dashboard/components/metric-shared";
import { formatByteRate } from "../../dashboard/components/usage-resources-card";

// Decimal units, matching formatByteRate's 1000-step ladder so the current
// value row and the IO chart tiles read the same numbers.
const BYTE_UNITS = [
  { divisor: 1, suffix: "B" },
  { divisor: 1_000, suffix: "KB" },
  { divisor: 1_000_000, suffix: "MB" },
  { divisor: 1_000_000_000, suffix: "GB" },
  { divisor: 1_000_000_000_000, suffix: "TB" },
] as const;

function formatBytes(n: number | null | undefined): string {
  if (n == null || !Number.isFinite(n)) return "—";
  const magnitude = Math.abs(n);
  let index = BYTE_UNITS.findLastIndex(({ divisor }) => magnitude >= divisor);
  index = Math.max(index, 0);
  const unit = BYTE_UNITS[index]!;
  const scaled = n / unit.divisor;
  return `${scaled >= 100 ? scaled.toFixed(0) : scaled.toFixed(1)}${unit.suffix}`;
}

/** Last sample of the (single-daemon) series list — the "now" reading. */
function lastOf(list: UsageMetricSeries[] | undefined): number | null {
  const series = list?.[list.length - 1];
  if (!series || series.points.length === 0) return null;
  return series.points[series.points.length - 1]!.v;
}

/**
 * One trend tile: the narrowed panel answers per host, so at most a handful
 * of labelled lines show (one per daemon) and no ranking cap is needed.
 * `rows` is pre-merged; a null tile renders the no-samples fallback.
 */
function TrendTile({
  title,
  series,
  labelOf,
  transform,
  formatY,
  formatTooltipValue,
}: {
  title: string;
  series: UsageMetricSeries[];
  labelOf?: (s: UsageMetricSeries) => string;
  transform?: (p: { t: number; v: number }) => number;
  formatY?: (v: number) => string;
  formatTooltipValue?: (v: number) => string;
}) {
  const { t } = useT("runtimes");
  const named = topNamedSeries(
    series,
    (s) => labelOf?.(s) ?? s.labels.daemon ?? "?",
    (s) =>
      Math.max(
        0,
        ...s.points.map((p) => (transform ? transform({ t: p.t, v: p.v }) : p.v)),
      ),
  );
  if (named.length === 0)
    return (
      <div className="min-w-0">
        <p className="mb-1 text-caption font-medium text-muted-foreground">{title}</p>
        <p className="text-caption text-muted-foreground">
          {t(($) => $.detail.resources.no_series)}
        </p>
      </div>
    );
  const rows = mergeMetricRows(
    named.map(({ series: s, name }) => ({
      name,
      points: s.points.map((p) => ({
        t: p.t,
        v: transform ? transform({ t: p.t, v: p.v }) : p.v,
      })),
    })),
  );
  return (
    <div className="min-w-0">
      <p className="mb-1 text-caption font-medium text-muted-foreground">{title}</p>
      <MetricLineChart
        rows={rows}
        series={named.map(({ name }, i) => ({ name, color: metricSeriesColor(i) }))}
        formatY={formatY}
        formatTooltipValue={formatTooltipValue}
      />
    </div>
  );
}

// One current-value cell: an uppercase micro label over the mono reading.
// The "—" dash is structural (no sample yet / unit not present on the host),
// not a translated string, so it stays out of the locale bundles.
function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="text-micro uppercase tracking-wide text-muted-foreground">{label}</p>
      <p className="truncate font-mono text-caption text-foreground">{value}</p>
    </div>
  );
}

export function RuntimeResourcesCard({ runtime }: { runtime: AgentRuntime }) {
  const { t } = useT("runtimes");
  const timeAgo = useTimeAgo();
  const [window, setWindow] = useState<UsageMetricWindow>("1h");
  const query = useQuery(runtimeResourcesOptions(runtime.id, window));
  const data = query.data;
  const series = data?.series ?? {};

  // Visible structured empties — in this card the reader must always be able
  // to tell WHY the panel is not full, so no state silently hides the card.
  if (query.isPending && !data) {
    return (
      <div
        className="rounded-lg border bg-card p-4"
        data-testid="runtime-resources-card"
      >
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <Activity className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
            <h4 className="text-body font-semibold">
              {t(($) => $.detail.resources.title)}
            </h4>
          </div>
          <MetricWindowSegmented value={window} onChange={setWindow} />
        </div>
        <div className="mt-3 grid gap-4 sm:grid-cols-2">
          <Skeleton className="aspect-[3/1] rounded-md" />
          <Skeleton className="aspect-[3/1] rounded-md" />
        </div>
      </div>
    );
  }

  if (query.isError) {
    return (
      <div
        className="rounded-lg border bg-card p-4"
        data-testid="runtime-resources-card"
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <Activity className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
            <h4 className="text-body font-semibold">
              {t(($) => $.detail.resources.title)}
            </h4>
          </div>
          <MetricWindowSegmented value={window} onChange={setWindow} />
        </div>
        <div className="mt-3 flex aspect-[3/1] max-h-40 flex-col items-center justify-center gap-2 rounded-md border border-dashed bg-muted/20 p-4 text-center">
          <p className="text-caption text-muted-foreground">
            {t(($) => $.detail.resources.error_hint)}
          </p>
          <Button variant="outline" size="sm" onClick={() => query.refetch()}>
            <RefreshCw className="h-3.5 w-3.5" />
            {t(($) => $.detail.resources.retry)}
          </Button>
        </div>
      </div>
    );
  }

  if (!data) return null;

  if (!data.configured) {
    return (
      <EmptyShell
        title={t(($) => $.detail.resources.empty_not_configured_title)}
        hint={t(($) => $.detail.resources.empty_not_configured_hint)}
        window={window}
        onWindow={setWindow}
      />
    );
  }
  if (!data.has_daemon) {
    return (
      <EmptyShell
        title={t(($) => $.detail.resources.empty_no_daemon_title)}
        hint={t(($) => $.detail.resources.empty_no_daemon_hint)}
        window={window}
        onWindow={setWindow}
      />
    );
  }
  const hasAny = Object.values(series).some((s) => s.length > 0);
  if (!hasAny) {
    return (
      <EmptyShell
        title={t(($) => $.detail.resources.empty_no_samples_title)}
        hint={t(($) => $.detail.resources.empty_no_samples_hint)}
        window={window}
        onWindow={setWindow}
      />
    );
  }

  // Current values — the newest sample of each series, plus host uptime
  // derived from node_boot_time_seconds (boot time, not online time: the
  // host may have booted long before this daemon ever connected).
  const cpuNow = lastOf(series.cpu_cores);
  const memTotal = lastOf(series.memory_total_bytes);
  const memAvail = lastOf(series.memory_available_bytes);
  const memUsedPct =
    memTotal != null && memAvail != null && memTotal > 0
      ? Math.round((1 - memAvail / memTotal) * 100)
      : null;
  const swapTotal = lastOf(series.swap_total_bytes);
  const swapFree = lastOf(series.swap_free_bytes);
  const swapUsed = swapTotal != null && swapFree != null ? swapTotal - swapFree : null;
  const swapUsedPct =
    swapTotal != null && swapFree != null && swapTotal > 0
      ? Math.round(((swapTotal - swapFree) / swapTotal) * 100)
      : null;
  const fsSize = lastOf(series.filesystem_size_bytes);
  const fsAvail = lastOf(series.filesystem_avail_bytes);
  const ioRead = lastOf(series.disk_read_bytes_per_second);
  const ioWritten = lastOf(series.disk_written_bytes_per_second);
  const bootTime = lastOf(series.boot_time_seconds);
  const uptimeSec =
    bootTime != null ? Math.max(0, Date.now() / 1000 - bootTime) : null;

  // Percent tiles need both legs of the fraction on the same stamp: the
  // used fraction is 1 - free/total, one point per sample timestamp.
  const usedPctSeries = (
    free: UsageMetricSeries[] | undefined,
    total: UsageMetricSeries[] | undefined,
  ): { name: string; points: { t: number; v: number }[] }[] => {
    const denomByDaemon = new Map((total ?? []).map((s) => [s.labels.daemon ?? "", s]));
    const out: { name: string; points: { t: number; v: number }[] }[] = [];
    for (const s of free ?? []) {
      const d = denomByDaemon.get(s.labels.daemon ?? "");
      if (!d) continue;
      const dByName = new Map(d.points.map((p) => [p.t, p.v]));
      out.push({
        name: s.labels.daemon ?? "?",
        points: s.points.map((p) => {
          const tot = dByName.get(p.t) ?? d.points[0]?.v ?? 0;
          return { t: p.t, v: tot > 0 ? Math.round((1 - p.v / tot) * 1000) / 10 : 0 };
        }),
      });
    }
    return out;
  };
  const memPctSeries = usedPctSeries(
    series.memory_available_bytes,
    series.memory_total_bytes,
  );
  const swapPctSeries = usedPctSeries(
    series.swap_free_bytes,
    series.swap_total_bytes,
  );

  return (
    <div className="rounded-lg border bg-card p-4" data-testid="runtime-resources-card">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          <h4 className="text-body font-semibold">
            {t(($) => $.detail.resources.title)}
          </h4>
          <span className="text-caption text-muted-foreground">
            {t(($) => $.detail.resources.live)}
          </span>
        </div>
        <MetricWindowSegmented value={window} onChange={setWindow} />
      </div>

      <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3 lg:grid-cols-4">
        <Fact
          label={t(($) => $.detail.resources.cpu)}
          value={
            cpuNow != null
              ? t(($) => $.detail.resources.cpu_value, { cores: cpuNow.toFixed(1) })
              : "—"
          }
        />
        <Fact
          label={t(($) => $.detail.resources.memory)}
          value={
            memUsedPct != null && memTotal != null
              ? t(($) => $.detail.resources.memory_value, {
                  used: formatBytes(memTotal - (memAvail ?? 0)),
                  total: formatBytes(memTotal),
                  pct: memUsedPct,
                })
              : "—"
          }
        />
        <Fact
          label={t(($) => $.detail.resources.swap)}
          value={
            swapTotal == null || swapTotal === 0
              ? t(($) => $.detail.resources.swap_none)
              : swapUsed != null && swapUsedPct != null
                ? t(($) => $.detail.resources.memory_value, {
                    used: formatBytes(swapUsed),
                    total: formatBytes(swapTotal),
                    pct: swapUsedPct,
                  })
                : "—"
          }
        />
        <Fact
          label={t(($) => $.detail.resources.disk)}
          value={
            fsSize != null && fsAvail != null
              ? t(($) => $.detail.resources.disk_value, {
                  avail: formatBytes(fsAvail),
                  total: formatBytes(fsSize),
                })
              : "—"
          }
        />
        <Fact
          label={t(($) => $.detail.resources.io)}
          value={
            ioRead != null || ioWritten != null
              ? t(($) => $.detail.resources.io_value, {
                  read: formatByteRate(ioRead ?? 0),
                  write: formatByteRate(ioWritten ?? 0),
                })
              : "—"
          }
        />
        <Fact
          label={t(($) => $.detail.resources.uptime)}
          value={
            uptimeSec != null
              ? formatDuration(uptimeSec, t(($) => $.detail.resources.uptime_less_minute))
              : "—"
          }
        />
        <Fact
          label={t(($) => $.detail.resources.sampled)}
          value={data.sampled_at ? timeAgo(data.sampled_at) : "—"}
        />
        <Fact
          label={t(($) => $.detail.resources.daemon)}
          value={data.daemon_id || "—"}
        />
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2" data-testid="runtime-resources-charts">
        <TrendTile
          title={t(($) => $.detail.resources.chart_cpu)}
          series={series.cpu_cores ?? []}
          formatY={(v) => v.toFixed(1)}
          formatTooltipValue={(v) => v.toFixed(2)}
        />
        {memPctSeries.length > 0 ? (
          <div className="min-w-0">
            <p className="mb-1 text-caption font-medium text-muted-foreground">
              {t(($) => $.detail.resources.chart_memory)}
            </p>
            <MetricLineChart
              rows={mergeMetricRows(memPctSeries)}
              series={memPctSeries.map((s, i) => ({
                name: s.name,
                color: metricSeriesColor(i),
              }))}
              formatY={(v) => `${Math.round(v)}%`}
              formatTooltipValue={(v) => `${v.toFixed(1)}%`}
            />
          </div>
        ) : (
          <TrendTile
            title={t(($) => $.detail.resources.chart_memory)}
            series={[] as UsageMetricSeries[]}
          />
        )}
        {swapPctSeries.length > 0 ? (
          <div className="min-w-0">
            <p className="mb-1 text-caption font-medium text-muted-foreground">
              {t(($) => $.detail.resources.chart_swap)}
            </p>
            <MetricLineChart
              rows={mergeMetricRows(swapPctSeries)}
              series={swapPctSeries.map((s, i) => ({
                name: s.name,
                color: metricSeriesColor(i),
              }))}
              formatY={(v) => `${Math.round(v)}%`}
              formatTooltipValue={(v) => `${v.toFixed(1)}%`}
            />
          </div>
        ) : (
          <TrendTile
            title={t(($) => $.detail.resources.chart_swap)}
            series={[] as UsageMetricSeries[]}
          />
        )}
        <TrendTile
          title={t(($) => $.detail.resources.chart_io)}
          series={[
            ...((series.disk_read_bytes_per_second ?? []).map((s) => ({
              ...s,
              labels: { ...s.labels, io_dir: "read" },
            }))),
            ...((series.disk_written_bytes_per_second ?? []).map((s) => ({
              ...s,
              labels: { ...s.labels, io_dir: "written" },
            }))),
          ]}
          labelOf={(s) =>
            `${s.labels.daemon ?? "?"} ${s.labels.io_dir ?? ""}`.trim()
          }
          formatY={formatByteRate}
          formatTooltipValue={formatByteRate}
        />
      </div>
    </div>
  );
}

// The three "no data" shells share one layout: the card header stays up
// (title + window switcher, so the reader sees the section exists and is
// deliberately empty) and the body is the named empty state, never nothing.
function EmptyShell({
  title,
  hint,
  window,
  onWindow,
}: {
  title: string;
  hint: string;
  window: UsageMetricWindow;
  onWindow: (w: UsageMetricWindow) => void;
}) {
  const { t } = useT("runtimes");
  return (
    <div className="rounded-lg border bg-card p-4" data-testid="runtime-resources-card">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          <h4 className="text-body font-semibold">
            {t(($) => $.detail.resources.title)}
          </h4>
        </div>
        <MetricWindowSegmented value={window} onChange={onWindow} />
      </div>
      <div
        className="mt-3 flex aspect-[3/1] max-h-40 flex-col items-center justify-center gap-1.5 rounded-md border border-dashed bg-muted/20 p-4 text-center"
        data-testid="runtime-resources-empty"
      >
        <p className="text-caption font-medium text-foreground">{title}</p>
        <p className="mx-auto max-w-md text-caption text-muted-foreground">{hint}</p>
      </div>
    </div>
  );
}
