"use client";

import { useMemo } from "react";
import {
  BarChart,
  Bar,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Cell,
  ReferenceLine,
} from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import { cn } from "@multica/ui/lib/utils";

// ─── Types ──────────────────────────────────────────────────────────────────

interface TimelineItem {
  seq: number;
  type: "tool_use" | "tool_result" | "thinking" | "text" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
}

// ─── Gantt chart ────────────────────────────────────────────────────────────

interface GanttSpan {
  tool: string;
  startSeq: number;
  endSeq: number;
  duration: number;
  color: string;
  type: "tool_use" | "thinking" | "text" | "error";
}

const TOOL_COLORS: Record<string, string> = {
  Bash: "hsl(var(--chart-1))",
  Read: "hsl(var(--chart-2))",
  Edit: "hsl(var(--chart-3))",
  Write: "hsl(var(--chart-4))",
  Grep: "hsl(var(--chart-5))",
  Glob: "hsl(var(--chart-1))",
  Agent: "hsl(210 80% 60%)",
  WebSearch: "hsl(280 60% 55%)",
  WebFetch: "hsl(280 60% 55%)",
  Skill: "hsl(330 60% 55%)",
};

const TYPE_COLORS: Record<string, string> = {
  thinking: "hsl(270 60% 65%)",
  text: "hsl(150 60% 45%)",
  error: "hsl(0 70% 55%)",
};

function getSpanColor(span: GanttSpan): string {
  if (span.type === "tool_use") {
    return TOOL_COLORS[span.tool] ?? "hsl(var(--chart-2))";
  }
  return TYPE_COLORS[span.type] ?? "hsl(var(--muted-foreground))";
}

function buildGanttSpans(items: TimelineItem[]): GanttSpan[] {
  const spans: GanttSpan[] = [];

  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;

    if (item.type === "tool_use") {
      // Find matching tool_result
      let endIdx = i + 1;
      while (endIdx < items.length && items[endIdx]!.type !== "tool_result") {
        endIdx++;
      }
      if (endIdx >= items.length) endIdx = i; // no result found, point span

      const span: GanttSpan = {
        tool: item.tool ?? "Tool",
        startSeq: item.seq,
        endSeq: items[endIdx]?.seq ?? item.seq,
        duration: endIdx - i,
        color: "",
        type: "tool_use",
      };
      span.color = getSpanColor(span);
      spans.push(span);
    } else if (item.type === "thinking" || item.type === "text" || item.type === "error") {
      const span: GanttSpan = {
        tool: item.type === "thinking" ? "Thinking" : item.type === "text" ? "Agent" : "Error",
        startSeq: item.seq,
        endSeq: item.seq,
        duration: 1,
        color: "",
        type: item.type,
      };
      span.color = getSpanColor(span);
      spans.push(span);
    }
  }

  return spans;
}

// Transform spans into bar chart data: each span becomes a stacked bar with offset
interface GanttBarData {
  label: string;
  tool: string;
  start: number;
  span: number;
  fill: string;
  type: string;
  seq: number;
}

const ganttConfig = {
  span: { label: "Duration (events)", color: "hsl(var(--chart-1))" },
} satisfies ChartConfig;

export function TranscriptGanttChart({
  items,
  onEventClick,
}: {
  items: TimelineItem[];
  onEventClick?: (seq: number) => void;
}) {
  const { barData, maxSeq } = useMemo(() => {
    const spans = buildGanttSpans(items);
    const data: GanttBarData[] = spans.map((s, idx) => ({
      label: `${s.tool}`,
      tool: s.tool,
      start: s.startSeq,
      span: Math.max(s.endSeq - s.startSeq, 1),
      fill: s.color,
      type: s.type,
      seq: s.startSeq,
    }));
    const ms = items.length > 0 ? items[items.length - 1]!.seq : 0;
    return { barData: data, maxSeq: ms };
  }, [items]);

  if (barData.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
        No tool calls to visualize.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-medium text-muted-foreground">Execution Gantt Chart</h4>
        <div className="flex items-center gap-3 text-[10px] text-muted-foreground">
          <span className="flex items-center gap-1">
            <span className="inline-block w-2 h-2 rounded-sm" style={{ background: "hsl(var(--chart-1))" }} />
            Tool calls
          </span>
          <span className="flex items-center gap-1">
            <span className="inline-block w-2 h-2 rounded-sm" style={{ background: TYPE_COLORS.thinking }} />
            Thinking
          </span>
          <span className="flex items-center gap-1">
            <span className="inline-block w-2 h-2 rounded-sm" style={{ background: TYPE_COLORS.text }} />
            Agent text
          </span>
          <span className="flex items-center gap-1">
            <span className="inline-block w-2 h-2 rounded-sm" style={{ background: TYPE_COLORS.error }} />
            Error
          </span>
        </div>
      </div>

      <ChartContainer config={ganttConfig} className="w-full" style={{ height: Math.min(barData.length * 28 + 40, 400) }}>
        <BarChart
          data={barData}
          layout="vertical"
          margin={{ left: 0, right: 8, top: 4, bottom: 4 }}
          barSize={16}
          onClick={(state: Record<string, unknown> | null) => {
            const ap = (state as { activePayload?: { payload?: { seq?: number } }[] } | null)?.activePayload;
            if (ap?.[0]?.payload?.seq && onEventClick) {
              onEventClick(ap[0].payload.seq);
            }
          }}
        >
          <CartesianGrid horizontal={false} strokeDasharray="3 3" />
          <XAxis
            type="number"
            domain={[0, maxSeq + 1]}
            tickLine={false}
            axisLine={false}
            tickMargin={4}
            tickFormatter={(v: number) => `#${v}`}
            fontSize={10}
          />
          <YAxis
            type="category"
            dataKey="label"
            tickLine={false}
            axisLine={false}
            width={70}
            fontSize={10}
            tick={({ x, y, payload }) => (
              <text x={x} y={y} dy={4} textAnchor="end" fill="currentColor" fontSize={10} className="fill-muted-foreground">
                {(payload.value as string).length > 10 ? (payload.value as string).slice(0, 10) + "…" : payload.value as string}
              </text>
            )}
          />
          <ChartTooltip
            content={
              <ChartTooltipContent
                hideIndicator
                formatter={(value, name, entry) => {
                  const d = entry.payload as GanttBarData;
                  return (
                    <div className="flex flex-col gap-0.5">
                      <span className="font-medium">{d.tool}</span>
                      <span className="text-muted-foreground">
                        #{d.seq} → #{d.seq + d.span} ({d.span} events)
                      </span>
                    </div>
                  );
                }}
              />
            }
          />
          {/* Invisible offset bar */}
          <Bar dataKey="start" stackId="gantt" fill="transparent" radius={0} isAnimationActive={false} />
          {/* Visible span bar */}
          <Bar dataKey="span" stackId="gantt" radius={[3, 3, 3, 3]} cursor="pointer">
            {barData.map((entry, index) => (
              <Cell key={index} fill={entry.fill} />
            ))}
          </Bar>
        </BarChart>
      </ChartContainer>
    </div>
  );
}

// ─── Token consumption curve ────────────────────────────────────────────────

interface TokenDataPoint {
  seq: number;
  label: string;
  tokens: number;
  cumulative: number;
  type: string;
  tool?: string;
}

function estimateTokens(item: TimelineItem): number {
  let chars = 0;
  if (item.content) chars += item.content.length;
  if (item.output) chars += item.output.length;
  if (item.input) chars += JSON.stringify(item.input).length;
  // Rough estimate: ~4 chars per token for English/code
  return Math.round(chars / 4);
}

function formatTokens(n: number): string {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return String(n);
}

const tokenCurveConfig = {
  cumulative: { label: "Cumulative Tokens", color: "hsl(var(--chart-1))" },
  tokens: { label: "Per Event", color: "hsl(var(--chart-3))" },
} satisfies ChartConfig;

export function TranscriptTokenChart({
  items,
  onEventClick,
}: {
  items: TimelineItem[];
  onEventClick?: (seq: number) => void;
}) {
  const { dataPoints, totalTokens, errorSeqs } = useMemo(() => {
    let cumulative = 0;
    const points: TokenDataPoint[] = [];
    const errors: number[] = [];

    for (const item of items) {
      const tokens = estimateTokens(item);
      cumulative += tokens;
      points.push({
        seq: item.seq,
        label: `#${item.seq}`,
        tokens,
        cumulative,
        type: item.type,
        tool: item.tool,
      });
      if (item.type === "error") {
        errors.push(item.seq);
      }
    }

    return { dataPoints: points, totalTokens: cumulative, errorSeqs: errors };
  }, [items]);

  if (dataPoints.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
        No events to analyze.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-medium text-muted-foreground">Token Consumption (estimated)</h4>
        <div className="flex items-center gap-3 text-[10px]">
          <span className="text-muted-foreground">
            Total: <span className="font-medium text-foreground">{formatTokens(totalTokens)}</span> tokens
          </span>
          <span className="text-muted-foreground">
            Avg: <span className="font-medium text-foreground">{formatTokens(Math.round(totalTokens / dataPoints.length))}</span>/event
          </span>
        </div>
      </div>

      <ChartContainer config={tokenCurveConfig} className="aspect-[3/1] w-full">
        <AreaChart
          data={dataPoints}
          margin={{ left: 0, right: 8, top: 4, bottom: 4 }}
          onClick={(state: Record<string, unknown> | null) => {
            const ap = (state as { activePayload?: { payload?: { seq?: number } }[] } | null)?.activePayload;
            if (ap?.[0]?.payload?.seq && onEventClick) {
              onEventClick(ap[0].payload.seq);
            }
          }}
        >
          <CartesianGrid vertical={false} strokeDasharray="3 3" />
          <XAxis
            dataKey="seq"
            tickLine={false}
            axisLine={false}
            tickMargin={4}
            tickFormatter={(v: number) => `#${v}`}
            interval="preserveStartEnd"
            fontSize={10}
          />
          <YAxis
            tickLine={false}
            axisLine={false}
            tickMargin={4}
            tickFormatter={(v: number) => formatTokens(v)}
            width={45}
            fontSize={10}
          />
          <ChartTooltip
            content={
              <ChartTooltipContent
                formatter={(value, name, entry) => {
                  const d = entry.payload as TokenDataPoint;
                  const eventLabel =
                    d.type === "tool_use"
                      ? d.tool ?? "Tool"
                      : d.type === "tool_result"
                        ? `${d.tool ?? "Tool"} result`
                        : d.type.charAt(0).toUpperCase() + d.type.slice(1);
                  return (
                    <div className="flex flex-col gap-0.5">
                      <span className="font-medium">#{d.seq} — {eventLabel}</span>
                      <span className="text-muted-foreground">
                        This event: {formatTokens(d.tokens)} · Cumulative: {formatTokens(d.cumulative)}
                      </span>
                    </div>
                  );
                }}
              />
            }
          />
          {/* Error markers */}
          {errorSeqs.map((seq) => (
            <ReferenceLine
              key={seq}
              x={seq}
              stroke="hsl(0 70% 55%)"
              strokeDasharray="3 3"
              strokeWidth={1}
            />
          ))}
          <Area
            type="monotone"
            dataKey="cumulative"
            stroke="var(--color-cumulative)"
            fill="var(--color-cumulative)"
            fillOpacity={0.15}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 3, cursor: "pointer" }}
          />
        </AreaChart>
      </ChartContainer>
    </div>
  );
}
