"use client";

import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity } from "lucide-react";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { dashboardUsageResourcesOptions, type UsageMetricWindow } from "@multica/core/dashboard";
import type { UsageMetricSeries } from "@multica/core/types";
import { useT } from "../../i18n";
import {
  MetricLineChart,
  MetricPanelMessage,
  MetricWindowSegmented,
  mergeMetricRows,
  meanOf,
  metricSeriesColor,
  topNamedSeries,
} from "./metric-shared";

/**
 * System-resources panel of the usage page: per-daemon host CPU, memory and
 * disk IO, relayed by daemon heartbeats into Prometheus and proxied back
 * through the server. Self-contained on purpose — the live panels run on
 * their own relative window ("1h".."7d"), independent of the calendar-day
 * window the page header drives, so their state lives here and not in the
 * page's filter bar.
 *
 * configured=false (deployment without the observability stack) renders
 * nothing at all — that state is normal, not an error the reader should see.
 */
export function UsageResourcesCard({ wsId }: { wsId: string }) {
  const { t } = useT("usage");
  const [window, setWindow] = useState<UsageMetricWindow>("1h");
  const query = useQuery(dashboardUsageResourcesOptions(wsId, window));

  if (query.data && !query.data.configured) return null;

  const series = query.data?.series ?? {};
  const hasAny = Object.values(series).some((s) => s.length > 0);

  return (
    <div className="rounded-lg border bg-card p-4" data-testid="usage-resources-card">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-muted-foreground" />
          <h4 className="text-body font-semibold">
            {t(($) => $.metrics.resources.title)}
          </h4>
          <span className="text-caption text-muted-foreground">
            {t(($) => $.metrics.live_label)}
          </span>
        </div>
        <MetricWindowSegmented value={window} onChange={setWindow} />
      </div>
      {query.isPending ? (
        <div className="grid gap-4 lg:grid-cols-3">
          <Skeleton className="aspect-[3/1] rounded-md" />
          <Skeleton className="aspect-[3/1] rounded-md" />
          <Skeleton className="aspect-[3/1] rounded-md" />
        </div>
      ) : query.isError ? (
        <MetricPanelMessage text={t(($) => $.metrics.error)} />
      ) : !hasAny ? (
        <MetricPanelMessage text={t(($) => $.metrics.no_data)} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-3">
          <CpuChart series={series.cpu_cores ?? []} />
          <MemoryChart
            used={series.memory_available_bytes ?? []}
            total={series.memory_total_bytes ?? []}
          />
          <DiskChart
            read={series.disk_read_bytes_per_second ?? []}
            written={series.disk_written_bytes_per_second ?? []}
          />
        </div>
      )}
    </div>
  );
}

function ChartTile({
  title,
  chart,
}: {
  title: string;
  chart: ReactNode;
}) {
  const { t } = useT("usage");
  return (
    <div className="min-w-0">
      <p className="mb-1 text-caption font-medium text-muted-foreground">{title}</p>
      {chart ?? (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.metrics.no_data)}
        </p>
      )}
    </div>
  );
}

function CpuChart({ series }: { series: UsageMetricSeries[] }) {
  const { t } = useT("usage");
  const named = topNamedSeries(
    series,
    (s) => s.labels.daemon ?? "?",
    (s) => meanOf(s.points.map((p) => p.v)),
  );
  if (named.length === 0) return <ChartTile title={t(($) => $.metrics.resources.cpu)} chart={null} />;
  const rows = mergeMetricRows(
    named.map(({ series: s, name }) => ({ name, points: s.points })),
  );
  return (
    <ChartTile
      title={t(($) => $.metrics.resources.cpu)}
      chart={
        <MetricLineChart
          rows={rows}
          series={named.map(({ name }, i) => ({ name, color: metricSeriesColor(i) }))}
          formatY={(v) => v.toFixed(1)}
          formatTooltipValue={(v) => v.toFixed(2)}
        />
      }
    />
  );
}

function MemoryChart({
  used,
  total,
}: {
  used: UsageMetricSeries[];
  total: UsageMetricSeries[];
}) {
  const { t } = useT("usage");
  // Per-daemon percent used: available is "still free", so used fraction is
  // 1 - available/total. Percent — not bytes — so hosts of different sizes
  // share one readable axis.
  const totalOf = new Map(total.map((s) => [s.labels.daemon ?? "", s]));
  type PointList = UsageMetricSeries["points"];
  const perDaemon = used.flatMap((s) => {
    const daemon = s.labels.daemon ?? "";
    const tot = totalOf.get(daemon);
    if (!tot) return [];
    return [{ daemon: daemon || "?", points: s.points as PointList, tot }];
  });
  const named = topNamedSeries(perDaemon, (d) => d.daemon, (d) =>
    meanOf(
      d.points.map((p) => {
        const totAt = d.tot.points.find((tp) => tp.t === p.t)?.v ?? d.tot.points[0]?.v ?? 0;
        return totAt > 0 ? 1 - p.v / totAt : 0;
      }),
    ),
  );
  if (named.length === 0)
    return <ChartTile title={t(($) => $.metrics.resources.memory)} chart={null} />;
  const rows = mergeMetricRows(
    named.map(({ series: d, name }) => ({
      name,
      points: d.points.map((p) => {
        const totAt = d.tot.points.find((tp) => tp.t === p.t)?.v ?? d.tot.points[0]?.v ?? 0;
        return { t: p.t, v: totAt > 0 ? (1 - p.v / totAt) * 100 : 0 };
      }),
    })),
  );
  return (
    <ChartTile
      title={t(($) => $.metrics.resources.memory)}
      chart={
        <MetricLineChart
          rows={rows}
          series={named.map(({ name }, i) => ({ name, color: metricSeriesColor(i) }))}
          formatY={(v) => `${Math.round(v)}%`}
          formatTooltipValue={(v) => `${v.toFixed(1)}%`}
        />
      }
    />
  );
}

function DiskChart({
  read,
  written,
}: {
  read: UsageMetricSeries[];
  written: UsageMetricSeries[];
}) {
  const { t } = useT("usage");
  const named = topNamedSeries(
    [
      ...read.map((s) => ({ s, dir: "read" as const })),
      ...written.map((s) => ({ s, dir: "written" as const })),
    ],
    (e) => `${e.s.labels.daemon ?? "?"} ${e.dir}`,
    (e) => meanOf(e.s.points.map((p) => p.v)),
  );
  if (named.length === 0)
    return <ChartTile title={t(($) => $.metrics.resources.disk)} chart={null} />;
  const rows = mergeMetricRows(
    named.map(({ series: e, name }) => ({ name, points: e.s.points })),
  );
  return (
    <ChartTile
      title={t(($) => $.metrics.resources.disk)}
      chart={
        <MetricLineChart
          rows={rows}
          series={named.map(({ name }, i) => ({ name, color: metricSeriesColor(i) }))}
          formatY={formatByteRate}
          formatTooltipValue={formatByteRate}
        />
      }
    />
  );
}

const BYTE_UNITS = [
  { divisor: 1, suffix: "B" },
  { divisor: 1_000, suffix: "KB" },
  { divisor: 1_000_000, suffix: "MB" },
  { divisor: 1_000_000_000, suffix: "GB" },
  { divisor: 1_000_000_000_000, suffix: "TB" },
] as const;

export function formatByteRate(n: number): string {
  const magnitude = Math.abs(n);
  let index = BYTE_UNITS.findLastIndex(({ divisor }) => magnitude >= divisor);
  index = Math.max(index, 0);
  const unit = BYTE_UNITS[index]!;
  const scaled = n / unit.divisor;
  return `${scaled >= 100 ? scaled.toFixed(0) : scaled.toFixed(1)}${unit.suffix}/s`;
}
