import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Cell,
} from "recharts";
import {
  useBarSelection,
  weeklyBarSpans,
  type BarSelection,
} from "./bar-selection";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import { useLocale, useT } from "../../../i18n";
import type { WeeklyTasksData } from "@multica/core/dashboard";

// Weekly counterpart of DailyTasksChart — same completed/cancelled/failed
// stacked bar, but each bar groups a Mon–Sun calendar week. Partial-week
// bars at half opacity match WeeklyCostChart / WeeklyTokensChart so the
// in-progress week reads as visually subordinate everywhere.
const weeklyTasksChartConfig = {
  completed: { label: "Completed", color: "var(--chart-1)" },
  cancelled: { label: "Cancelled", color: "var(--chart-3)" },
  failed: { label: "Failed", color: "var(--chart-5)" },
} satisfies ChartConfig;

// Data shape lives with its producer: `@multica/core/dashboard/aggregate`
// (RUYI-638 moved the dashboard aggregators to core so mobile shares them).
export type { WeeklyTasksData } from "@multica/core/dashboard";

export function WeeklyTasksChart({
  data,
  selection,
  onSelectionChange,
}: {
  data: WeeklyTasksData[];
  selection?: BarSelection | null;
  onSelectionChange?: (next: BarSelection | null) => void;
}) {
  const { t } = useT("usage");
  const { t: tRuntimes } = useT("runtimes");
  const locale = useLocale();
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
    <ChartContainer
      config={weeklyTasksChartConfig}
      className="aspect-[3/1] w-full"
    >
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
                const row = payload[0]?.payload as WeeklyTasksData | undefined;
                if (!row) return "";
                return row.partial
                  ? t(($) => $.weekly.partial_label, {
                      range: row.rangeLabel,
                      covered: row.daysCovered,
                    })
                  : row.rangeLabel;
              }}
              formatter={(value, name) => `${value} ${name}`}
              footer={(payload) => {
                const total = payload.reduce(
                  (sum, item) =>
                    sum +
                    (typeof item.value === "number" ? item.value : 0),
                  0,
                );
                return (
                  <div className="flex items-center justify-between gap-2 font-medium">
                    <span>{tRuntimes(($) => $.charts.tooltip_total)}</span>
                    <span className="font-mono tabular-nums">
                      {total.toLocaleString(locale)}
                    </span>
                  </div>
                );
              }}
            />
          }
        />
        <Bar
          dataKey="completed"
          stackId="tasks"
          fill="var(--color-completed)"
          radius={[0, 0, 0, 0]}
        >
          {barCells}
        </Bar>
        <Bar
          dataKey="cancelled"
          stackId="tasks"
          fill="var(--color-cancelled)"
          radius={[0, 0, 0, 0]}
        >
          {barCells}
        </Bar>
        <Bar
          dataKey="failed"
          stackId="tasks"
          fill="var(--color-failed)"
          radius={[3, 3, 0, 0]}
        >
          {barCells}
        </Bar>
      </BarChart>
    </ChartContainer>
  );
}
