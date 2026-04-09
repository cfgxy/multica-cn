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
  ReferenceLine,
  Tooltip,
} from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";

// ─── Types ──────────────────────────────────────────────────────────────────

interface TimelineItem {
  seq: number;
  type: "tool_use" | "tool_result" | "thinking" | "text" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
}

// ─── Shared helpers ─────────────────────────────────────────────────────────

function shortenPath(p: string): string {
  const parts = p.split("/");
  if (parts.length <= 3) return p;
  return "…/" + parts.slice(-2).join("/");
}

function getToolSummary(item: TimelineItem): string {
  if (!item.input) return "";
  const inp = item.input as Record<string, string>;
  if (inp.query) return inp.query;
  if (inp.file_path) return shortenPath(inp.file_path);
  if (inp.path) return shortenPath(inp.path);
  if (inp.pattern) return inp.pattern;
  if (inp.description) return String(inp.description).slice(0, 50);
  if (inp.command) return String(inp.command).slice(0, 50);
  if (inp.prompt) return String(inp.prompt).slice(0, 50);
  if (inp.skill) return String(inp.skill);
  return "";
}

function formatTokens(n: number): string {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return String(n);
}

// ─── Gantt chart (recharts range bars) ──────────────────────────────────────

const TOOL_COLORS: Record<string, string> = {
  Bash: "hsl(220 70% 55%)",
  Read: "hsl(150 60% 45%)",
  Edit: "hsl(35 80% 50%)",
  Write: "hsl(280 55% 55%)",
  Grep: "hsl(180 60% 40%)",
  Glob: "hsl(200 60% 50%)",
  Agent: "hsl(260 60% 55%)",
  WebSearch: "hsl(320 55% 50%)",
  WebFetch: "hsl(320 55% 50%)",
  Skill: "hsl(340 55% 50%)",
  TodoWrite: "hsl(60 60% 45%)",
};

const TYPE_COLORS: Record<string, string> = {
  thinking: "hsl(270 50% 65%)",
  text: "hsl(150 55% 50%)",
  error: "hsl(0 70% 55%)",
};

interface GanttRow {
  name: string;
  range: [number, number];
  fill: string;
  seq: number;
  summary: string;
}

function buildGanttRows(items: TimelineItem[]): GanttRow[] {
  const rows: GanttRow[] = [];

  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;

    if (item.type === "tool_use") {
      // Find matching tool_result
      let endIdx = i + 1;
      while (endIdx < items.length && items[endIdx]!.type !== "tool_result") {
        endIdx++;
      }
      const endSeq = endIdx < items.length ? items[endIdx]!.seq : item.seq + 1;
      const toolName = item.tool ?? "Tool";
      const summary = getToolSummary(item);
      const label = summary ? `${toolName}: ${summary}` : toolName;

      rows.push({
        name: label.length > 35 ? label.slice(0, 35) + "…" : label,
        range: [item.seq, Math.max(endSeq, item.seq + 0.5)],
        fill: TOOL_COLORS[toolName] ?? "hsl(210 50% 55%)",
        seq: item.seq,
        summary,
      });
    } else if (item.type === "thinking") {
      rows.push({
        name: "Thinking",
        range: [item.seq, item.seq + 0.4],
        fill: TYPE_COLORS.thinking!,
        seq: item.seq,
        summary: item.content?.slice(0, 50) ?? "",
      });
    } else if (item.type === "text") {
      const last = item.content?.split("\n").filter(Boolean).pop()?.slice(0, 30) ?? "";
      rows.push({
        name: last ? `Agent: ${last}` : "Agent",
        range: [item.seq, item.seq + 0.4],
        fill: TYPE_COLORS.text!,
        seq: item.seq,
        summary: last,
      });
    } else if (item.type === "error") {
      rows.push({
        name: "Error",
        range: [item.seq, item.seq + 0.4],
        fill: TYPE_COLORS.error!,
        seq: item.seq,
        summary: item.content?.slice(0, 50) ?? "",
      });
    }
  }

  return rows;
}

// Custom bar shape that reads fill from data
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function GanttBarShape(props: any) {
  const { x, y, width, height, payload } = props as {
    x: number;
    y: number;
    width: number;
    height: number;
    payload: GanttRow;
  };
  return (
    <rect
      x={x}
      y={y + 2}
      width={Math.max(width, 4)}
      height={height - 4}
      rx={3}
      ry={3}
      fill={payload.fill}
      cursor="pointer"
    />
  );
}

const ganttConfig = {
  range: { label: "Event span", color: "hsl(var(--chart-1))" },
} satisfies ChartConfig;

export function TranscriptGanttChart({
  items,
  onEventClick,
}: {
  items: TimelineItem[];
  onEventClick?: (seq: number) => void;
}) {
  const { rows, maxSeq } = useMemo(() => {
    const r = buildGanttRows(items);
    const max = items.length > 0 ? items[items.length - 1]!.seq + 1 : 1;
    return { rows: r, maxSeq: max };
  }, [items]);

  if (rows.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
        No events to visualize.
      </div>
    );
  }

  // Build legend from unique tool types
  const legendEntries = useMemo(() => {
    const seen = new Map<string, string>();
    for (const r of rows) {
      // Extract base tool name from the row name
      const base = r.name.split(":")[0]!.trim();
      if (!seen.has(base)) seen.set(base, r.fill);
    }
    return Array.from(seen.entries());
  }, [rows]);

  const chartHeight = Math.min(rows.length * 28 + 50, 500);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-medium text-muted-foreground">Execution Gantt Chart</h4>
        <div className="flex items-center gap-2 flex-wrap text-[10px] text-muted-foreground">
          {legendEntries.map(([name, color]) => (
            <span key={name} className="flex items-center gap-1">
              <span className="inline-block w-2 h-2 rounded-sm" style={{ background: color }} />
              {name}
            </span>
          ))}
        </div>
      </div>

      <ChartContainer config={ganttConfig} className="w-full" style={{ height: chartHeight }}>
        <BarChart
          data={rows}
          layout="vertical"
          margin={{ left: 4, right: 12, top: 4, bottom: 4 }}
          barSize={20}
          onClick={(state: Record<string, unknown> | null) => {
            const ap = (state as { activePayload?: { payload?: GanttRow }[] } | null)?.activePayload;
            if (ap?.[0]?.payload?.seq != null && onEventClick) {
              onEventClick(ap[0].payload.seq);
            }
          }}
        >
          <CartesianGrid horizontal={false} strokeDasharray="3 3" />
          <XAxis
            type="number"
            domain={[0, maxSeq]}
            tickLine={false}
            axisLine={false}
            tickMargin={4}
            tickFormatter={(v: number) => `#${v}`}
            fontSize={10}
          />
          <YAxis
            type="category"
            dataKey="name"
            tickLine={false}
            axisLine={false}
            width={140}
            fontSize={10}
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            tick={(props: any) => {
              const { x, y, payload } = props as { x: number; y: number; payload: { value: string } };
              return (
                <text
                  x={x}
                  y={y}
                  dy={4}
                  textAnchor="end"
                  fontSize={10}
                  className="fill-muted-foreground"
                >
                  {payload.value}
                </text>
              );
            }}
          />
          <Tooltip
            content={({ active, payload }) => {
              if (!active || !payload?.[0]) return null;
              const d = payload[0].payload as GanttRow;
              return (
                <div className="rounded-lg border bg-background px-3 py-2 text-xs shadow-md">
                  <div className="font-medium">{d.name}</div>
                  <div className="text-muted-foreground mt-0.5">
                    #{d.range[0]} → #{d.range[1]} ({Math.round((d.range[1] - d.range[0]) * 10) / 10} events)
                  </div>
                </div>
              );
            }}
          />
          <Bar
            dataKey="range"
            shape={GanttBarShape}
            isAnimationActive={false}
          />
        </BarChart>
      </ChartContainer>
    </div>
  );
}

// ─── Token consumption curve (input/output split, no price) ─────────────────

// Token estimation: separate input (fed to model) vs output (generated by model)
// Note: These are rough estimates based on content length (~4 chars per token).
// Actual token counts are tracked in the task_usage table per task.
function estimateInputTokens(item: TimelineItem): number {
  let chars = 0;
  // tool_result content is fed back to the model as input
  if (item.type === "tool_result" && item.output) chars += item.output.length;
  // errors are fed back to the model
  if (item.type === "error" && item.content) chars += item.content.length;
  return Math.round(chars / 4);
}

function estimateOutputTokens(item: TimelineItem): number {
  let chars = 0;
  // text, thinking = model-generated output
  if ((item.type === "text" || item.type === "thinking") && item.content) {
    chars += item.content.length;
  }
  // tool_use = model generates the tool call (input params)
  if (item.type === "tool_use" && item.input) {
    chars += JSON.stringify(item.input).length;
  }
  return Math.round(chars / 4);
}

interface TokenDataPoint {
  seq: number;
  inputTokens: number;
  outputTokens: number;
  cumulativeInput: number;
  cumulativeOutput: number;
  cumulativeTotal: number;
  type: string;
  tool?: string;
}

const tokenCurveConfig = {
  cumulativeInput: { label: "Input (cumulative)", color: "hsl(var(--chart-1))" },
  cumulativeOutput: { label: "Output (cumulative)", color: "hsl(var(--chart-3))" },
} satisfies ChartConfig;

export function TranscriptTokenChart({
  items,
  onEventClick,
}: {
  items: TimelineItem[];
  onEventClick?: (seq: number) => void;
}) {
  const { dataPoints, totalInput, totalOutput, errorSeqs } = useMemo(() => {
    let cumInput = 0;
    let cumOutput = 0;
    const points: TokenDataPoint[] = [];
    const errors: number[] = [];

    for (const item of items) {
      const inp = estimateInputTokens(item);
      const out = estimateOutputTokens(item);
      cumInput += inp;
      cumOutput += out;
      points.push({
        seq: item.seq,
        inputTokens: inp,
        outputTokens: out,
        cumulativeInput: cumInput,
        cumulativeOutput: cumOutput,
        cumulativeTotal: cumInput + cumOutput,
        type: item.type,
        tool: item.tool,
      });
      if (item.type === "error") {
        errors.push(item.seq);
      }
    }

    return {
      dataPoints: points,
      totalInput: cumInput,
      totalOutput: cumOutput,
      errorSeqs: errors,
    };
  }, [items]);

  if (dataPoints.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
        No events to analyze.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-medium text-muted-foreground">Token Consumption (estimated from content length)</h4>
        <div className="flex items-center gap-3 text-[10px]">
          <span className="text-muted-foreground">
            Input: <span className="font-medium text-foreground">~{formatTokens(totalInput)}</span>
          </span>
          <span className="text-muted-foreground">
            Output: <span className="font-medium text-foreground">~{formatTokens(totalOutput)}</span>
          </span>
          <span className="text-muted-foreground">
            Total: <span className="font-medium text-foreground">~{formatTokens(totalInput + totalOutput)}</span>
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
                        In: ~{formatTokens(d.inputTokens)} · Out: ~{formatTokens(d.outputTokens)}
                      </span>
                      <span className="text-muted-foreground">
                        Cumulative: ~{formatTokens(d.cumulativeTotal)}
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
            dataKey="cumulativeInput"
            stackId="tokens"
            stroke="var(--color-cumulativeInput)"
            fill="var(--color-cumulativeInput)"
            fillOpacity={0.3}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 3, cursor: "pointer" }}
          />
          <Area
            type="monotone"
            dataKey="cumulativeOutput"
            stackId="tokens"
            stroke="var(--color-cumulativeOutput)"
            fill="var(--color-cumulativeOutput)"
            fillOpacity={0.2}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 3, cursor: "pointer" }}
          />
        </AreaChart>
      </ChartContainer>
    </div>
  );
}
