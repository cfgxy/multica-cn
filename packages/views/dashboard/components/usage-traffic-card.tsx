"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { TrendingUp } from "lucide-react";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { dashboardUsageTrafficOptions, type UsageMetricWindow } from "@multica/core/dashboard";
import type { DashboardUsageTrafficDim, UsageMetricSeries } from "@multica/core/types";
import { useT } from "../../i18n";
import { formatTokens } from "../../runtimes/utils";
import { Segmented } from "./dashboard-shared";
import {
  MetricLineChart,
  MetricPanelMessage,
  MetricWindowSegmented,
  mergeMetricRows,
  meanOf,
  metricSeriesColor,
  topNamedSeries,
} from "./metric-shared";

type TrafficDim = DashboardUsageTrafficDim;

/**
 * Model-traffic panel of the usage page: live token / cost / task *rates*
 * aggregated from task_usage into Prometheus, broken down by one dimension
 * (runtime, provider or model). Rates — not cumulative sums — because the
 * question this panel answers is "how hard is the fleet working right now",
 * which the daily rollup cards on the same tab can't show.
 *
 * Display units follow readability, not the wire: tokens stay per-second
 * (the observability convention), while cost and task counts are so small
 * per-second that per-hour reads better ($12/h, not $0.0033/s).
 */
export function UsageTrafficCard({ wsId }: { wsId: string }) {
  const { t } = useT("usage");
  const [window, setWindow] = useState<UsageMetricWindow>("1h");
  const [by, setBy] = useState<TrafficDim>("provider");
  const query = useQuery(dashboardUsageTrafficOptions(wsId, window, by));

  if (query.data && !query.data.configured) return null;

  const series = query.data?.series ?? {};
  const hasAny = Object.values(series).some((s) => s.length > 0);
  const dimLabelOf = (labels: Record<string, string>): string => {
    const raw = labels[by] ?? "?";
    // A runtime_id label is a bare UUID; a short prefix is enough to tell
    // lines apart, and the tooltip still shows the full id.
    return by === "runtime" ? raw.slice(0, 8) : raw;
  };

  const dimOptions: { label: string; value: TrafficDim }[] = [
    { label: t(($) => $.metrics.traffic.dim_runtime), value: "runtime" },
    { label: t(($) => $.metrics.traffic.dim_provider), value: "provider" },
    { label: t(($) => $.metrics.traffic.dim_model), value: "model" },
  ];

  return (
    <div className="rounded-lg border bg-card p-4" data-testid="usage-traffic-card">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <TrendingUp className="h-4 w-4 text-muted-foreground" />
          <h4 className="text-body font-semibold">
            {t(($) => $.metrics.traffic.title)}
          </h4>
          <span className="text-caption text-muted-foreground">
            {t(($) => $.metrics.live_label)}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Segmented
            label={t(($) => $.metrics.traffic.by_label)}
            value={by}
            onChange={setBy}
            options={dimOptions}
          />
          <MetricWindowSegmented value={window} onChange={setWindow} />
        </div>
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
          <TrafficTile
            title={t(($) => $.metrics.traffic.tokens)}
            input={series.tokens_per_second ?? []}
            nameOf={(labels) => dimLabelOf(labels)}
            formatY={formatTokens}
            formatTooltipValue={(v) => `${formatTokens(v)}/s`}
          />
          <TrafficTile
            title={t(($) => $.metrics.traffic.cost)}
            input={series.cost_usd_per_second ?? []}
            nameOf={(labels) => dimLabelOf(labels)}
            scale={3600}
            formatY={(v) => `$${v >= 10 ? v.toFixed(0) : v.toFixed(2)}`}
            formatTooltipValue={(v) => `$${v.toFixed(2)}/h`}
          />
          <TrafficTile
            title={t(($) => $.metrics.traffic.tasks)}
            input={series.tasks_per_second ?? []}
            nameOf={(labels) => dimLabelOf(labels)}
            scale={3600}
            formatY={(v) => (v >= 10 ? v.toFixed(0) : v.toFixed(1))}
            formatTooltipValue={(v) => `${v.toFixed(1)}/h`}
          />
        </div>
      )}
    </div>
  );
}

function TrafficTile({
  title,
  input,
  nameOf,
  scale = 1,
  formatY,
  formatTooltipValue,
}: {
  title: string;
  input: UsageMetricSeries[];
  nameOf: (labels: Record<string, string>) => string;
  // Per-second wire values scaled into the display unit (1 for tokens/s,
  // 3600 for the per-hour panels).
  scale?: number;
  formatY: (v: number) => string;
  formatTooltipValue: (v: number) => string;
}) {
  const { t } = useT("usage");
  const named = topNamedSeries(input, (s) => nameOf(s.labels), (s) =>
    meanOf(s.points.map((p) => p.v)),
  );
  if (named.length === 0)
    return (
      <div className="min-w-0">
        <p className="mb-1 text-caption font-medium text-muted-foreground">{title}</p>
        <p className="text-caption text-muted-foreground">{t(($) => $.metrics.no_data)}</p>
      </div>
    );
  const rows = mergeMetricRows(
    named.map(({ series: s, name }) => ({
      name,
      points: s.points.map((p) => ({ t: p.t, v: p.v * scale })),
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
