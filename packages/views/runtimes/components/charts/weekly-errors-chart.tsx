import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Cell } from "recharts";
import {
  useBarSelection,
  weeklyBarSpans,
  type BarSelection,
} from "./bar-selection";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@multica/ui/components/ui/chart";
import { useT } from "../../../i18n";
import {
  activeFailureClasses,
  formatRate,
  labelOf,
  useFailureClassConfig,
  type FailureBucketTotals,
  type FailureClassCounts,
} from "./failure-class-visuals";

export interface WeeklyErrorsData extends FailureClassCounts, FailureBucketTotals {
  weekStart: string;
  weekEnd: string;
  label: string;
  rangeLabel: string;
  partial: boolean;
  daysCovered: number;
}

/**
 * Weekly counterpart of DailyErrorsChart — same class stack, each bar a
 * Mon–Sun calendar week. Partial-week bars render at half opacity, matching
 * every other weekly chart so the in-progress week reads as subordinate.
 */
export function WeeklyErrorsChart({
  data,
  selection,
  onSelectionChange,
}: {
  data: WeeklyErrorsData[];
  selection?: BarSelection | null;
  onSelectionChange?: (next: BarSelection | null) => void;
}) {
  const { t } = useT("usage");
  const config = useFailureClassConfig();
  const classes = activeFailureClasses(data);
  const barSel = useBarSelection({
    spans: weeklyBarSpans(data),
    selection,
    onSelectionChange,
  });
  // Selection active -> the hook owns per-bar opacity (dim vs selected,
  // merged over the partial-week fade); idle -> fall back to this chart's
  // own partial Cells so the no-selection render is unchanged.
  const barCells =
    barSel.cells(data.length, {
      keyOf: (i) => data[i]?.weekStart ?? String(i),
      baseOpacity: (i) => (data[i]?.partial ? 0.5 : 1),
    }) ??
    data.map((d) => (
      <Cell key={d.weekStart} fillOpacity={d.partial ? 0.5 : 1} />
    ));


  return (
    <ChartContainer config={config} className="aspect-[3/1] w-full">
      <BarChart
        data={data}
        margin={{ left: 0, right: 0, top: 4, bottom: 0 }}
        {...barSel.chartProps}
      >
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="label"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          interval="preserveStartEnd"
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          allowDecimals={false}
          width="auto"
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelKey="rangeLabel"
              labelFormatter={(_label, payload) => {
                const row = payload[0]?.payload as WeeklyErrorsData | undefined;
                if (!row) return "";
                return row.partial
                  ? t(($) => $.weekly.partial_label, {
                      range: row.rangeLabel,
                      covered: row.daysCovered,
                    })
                  : row.rangeLabel;
              }}
              // `name` is the Recharts dataKey — the raw class id
              // ("rate_limit"). Resolve it through the chart config so the
              // tooltip shows the translated label the legend already uses.
              formatter={(value, name) => `${value} ${labelOf(config, name)}`}
              footer={(payload) => {
                const row = payload[0]?.payload as WeeklyErrorsData | undefined;
                if (!row) return null;
                return (
                  <div className="flex items-center justify-between gap-2 font-medium">
                    <span>{t(($) => $.errors.tooltip_rate)}</span>
                    <span className="font-mono tabular-nums">
                      {formatRate(row.failed, row.total)}
                    </span>
                  </div>
                );
              }}
            />
          }
        />
        {classes.map((c, i) => (
          <Bar
            key={c}
            dataKey={c}
            stackId="errors"
            fill={`var(--color-${c})`}
            radius={i === classes.length - 1 ? [3, 3, 0, 0] : [0, 0, 0, 0]}
          >
            {barCells}
          </Bar>
        ))}
      </BarChart>
    </ChartContainer>
  );
}
