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
import { useT } from "../../../i18n";
import type { WeeklyTimeData } from "@multica/core/dashboard";

// Weekly counterpart of DailyTimeChart — same single-series bar, but each
// bar represents Mon–Sun run-time totals. Partial weeks render at half
// opacity and tag their tooltip with "(partial · N / 7 days)" so the user
// can't misread an in-progress week as a sudden drop.
const weeklyTimeChartConfig = {
  totalSeconds: { label: "Run time", color: "var(--chart-1)" },
} satisfies ChartConfig;

// Data shape lives with its producer: `@multica/core/dashboard/aggregate`
// (RUYI-638 moved the dashboard aggregators to core so mobile shares them).
export type { WeeklyTimeData } from "@multica/core/dashboard";

export function WeeklyTimeChart({
  data,
  formatY,
  formatTooltip,
  selection,
  onSelectionChange,
}: {
  data: WeeklyTimeData[];
  formatY: (seconds: number) => string;
  formatTooltip: (seconds: number) => string;
  selection?: BarSelection | null;
  onSelectionChange?: (next: BarSelection | null) => void;
}) {
  const { t } = useT("usage");
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
      config={weeklyTimeChartConfig}
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
          tickFormatter={(v: number) => formatY(v)}
          width="auto"
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelKey="rangeLabel"
              labelFormatter={(_label, payload) => {
                const row = payload[0]?.payload as WeeklyTimeData | undefined;
                if (!row) return "";
                return row.partial
                  ? t(($) => $.weekly.partial_label, {
                      range: row.rangeLabel,
                      covered: row.daysCovered,
                    })
                  : row.rangeLabel;
              }}
              formatter={(value, name) =>
                typeof value === "number"
                  ? `${formatTooltip(value)} ${name}`
                  : `${value} ${name}`
              }
            />
          }
        />
        <Bar
          dataKey="totalSeconds"
          fill="var(--color-totalSeconds)"
          radius={[3, 3, 0, 0]}
        >
          {barCells}
        </Bar>
      </BarChart>
    </ChartContainer>
  );
}
