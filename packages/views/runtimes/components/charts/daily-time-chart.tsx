import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
} from "recharts";
import {
  useBarSelection,
  dailyBarSpans,
  type BarSelection,
} from "./bar-selection";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import type { DailyTimeData } from "@multica/core/dashboard";

// Single-series bar — total daily run time in seconds. The y-axis tick
// formatter and tooltip both use the same `formatDuration` so the user
// reads the same unit ladder (h / m / s) everywhere.
const timeChartConfig = {
  totalSeconds: { label: "Run time", color: "var(--chart-1)" },
} satisfies ChartConfig;

// Data shape lives with its producer: `@multica/core/dashboard/aggregate`
// (RUYI-638 moved the dashboard aggregators to core so mobile shares them).
export type { DailyTimeData } from "@multica/core/dashboard";
export function DailyTimeChart({
  data,
  formatY,
  formatTooltip,
  selection,
  onSelectionChange,
}: {
  data: DailyTimeData[];
  // Caller passes a `formatDuration`-style fn so the chart stays UI-string
  // agnostic (the "< 1m" fallback label is localized by the parent).
  formatY: (seconds: number) => string;
  formatTooltip: (seconds: number) => string;
  // Analytics-page selection wiring — optional so call sites without it
  // (the runtime usage section) render exactly as before.
  selection?: BarSelection | null;
  onSelectionChange?: (next: BarSelection | null) => void;
}) {
  const barSel = useBarSelection({
    spans: dailyBarSpans(data),
    selection,
    onSelectionChange,
  });
  const barCells = barSel.cells(data.length, {
    keyOf: (i) => data[i]?.date ?? String(i),
  });

  return (
    <ChartContainer config={timeChartConfig} className="aspect-[3/1] w-full">
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
