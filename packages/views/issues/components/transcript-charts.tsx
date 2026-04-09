"use client";

import { useMemo, useState } from "react";
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
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
  if (inp.description) return String(inp.description).slice(0, 60);
  if (inp.command) return String(inp.command).slice(0, 60);
  if (inp.prompt) return String(inp.prompt).slice(0, 60);
  if (inp.skill) return String(inp.skill);
  return "";
}

function formatTokens(n: number): string {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return String(n);
}

function formatCost(dollars: number): string {
  if (dollars < 0.01) return `<$0.01`;
  if (dollars < 1) return `$${dollars.toFixed(2)}`;
  return `$${dollars.toFixed(2)}`;
}

// ─── Gantt chart (CSS-based) ────────────────────────────────────────────────

const TOOL_COLORS: Record<string, string> = {
  Bash: "hsl(220, 70%, 55%)",
  Read: "hsl(150, 60%, 45%)",
  Edit: "hsl(35, 80%, 50%)",
  Write: "hsl(280, 55%, 55%)",
  Grep: "hsl(180, 60%, 40%)",
  Glob: "hsl(200, 60%, 50%)",
  Agent: "hsl(260, 60%, 55%)",
  WebSearch: "hsl(320, 55%, 50%)",
  WebFetch: "hsl(320, 55%, 50%)",
  Skill: "hsl(340, 55%, 50%)",
  TodoWrite: "hsl(60, 60%, 45%)",
};

const TYPE_COLORS: Record<string, string> = {
  thinking: "hsl(270, 50%, 65%)",
  text: "hsl(150, 55%, 50%)",
  error: "hsl(0, 70%, 55%)",
};

interface GanttSpan {
  label: string;
  tool: string;
  summary: string;
  startSeq: number;
  endSeq: number;
  color: string;
  type: string;
}

function buildGanttSpans(items: TimelineItem[]): GanttSpan[] {
  const spans: GanttSpan[] = [];

  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;

    if (item.type === "tool_use") {
      // Find matching tool_result (next one with same tool or just the next tool_result)
      let endIdx = i + 1;
      while (endIdx < items.length && items[endIdx]!.type !== "tool_result") {
        endIdx++;
      }
      const endSeq = endIdx < items.length ? items[endIdx]!.seq : item.seq;
      const toolName = item.tool ?? "Tool";
      const summary = getToolSummary(item);

      spans.push({
        label: toolName,
        tool: toolName,
        summary,
        startSeq: item.seq,
        endSeq,
        color: TOOL_COLORS[toolName] ?? "hsl(210, 50%, 55%)",
        type: "tool_use",
      });
    } else if (item.type === "thinking") {
      spans.push({
        label: "Thinking",
        tool: "Thinking",
        summary: item.content?.slice(0, 60) ?? "",
        startSeq: item.seq,
        endSeq: item.seq,
        color: TYPE_COLORS.thinking!,
        type: "thinking",
      });
    } else if (item.type === "text") {
      spans.push({
        label: "Agent",
        tool: "Agent",
        summary: item.content?.split("\n").filter(Boolean).pop()?.slice(0, 60) ?? "",
        startSeq: item.seq,
        endSeq: item.seq,
        color: TYPE_COLORS.text!,
        type: "text",
      });
    } else if (item.type === "error") {
      spans.push({
        label: "Error",
        tool: "Error",
        summary: item.content?.slice(0, 60) ?? "",
        startSeq: item.seq,
        endSeq: item.seq,
        color: TYPE_COLORS.error!,
        type: "error",
      });
    }
  }

  return spans;
}

export function TranscriptGanttChart({
  items,
  onEventClick,
}: {
  items: TimelineItem[];
  onEventClick?: (seq: number) => void;
}) {
  const [hoveredIdx, setHoveredIdx] = useState<number | null>(null);
  const { spans, minSeq, maxSeq } = useMemo(() => {
    const s = buildGanttSpans(items);
    const min = s.length > 0 ? Math.min(...s.map((sp) => sp.startSeq)) : 0;
    const max = s.length > 0 ? Math.max(...s.map((sp) => sp.endSeq)) : 0;
    return { spans: s, minSeq: min, maxSeq: max };
  }, [items]);

  if (spans.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
        No events to visualize.
      </div>
    );
  }

  const range = maxSeq - minSeq || 1;

  // Build unique legend entries from tool colors used
  const legendEntries = useMemo(() => {
    const seen = new Map<string, string>();
    for (const s of spans) {
      if (!seen.has(s.label)) seen.set(s.label, s.color);
    }
    return Array.from(seen.entries());
  }, [spans]);

  // X-axis tick marks
  const ticks = useMemo(() => {
    const step = Math.max(1, Math.round(range / 10));
    const t: number[] = [];
    for (let v = minSeq; v <= maxSeq; v += step) {
      t.push(v);
    }
    if (t[t.length - 1] !== maxSeq) t.push(maxSeq);
    return t;
  }, [minSeq, maxSeq, range]);

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

      {/* Chart body */}
      <div className="max-h-[400px] overflow-y-auto">
        <div className="space-y-0.5">
          {spans.map((span, idx) => {
            const leftPercent = ((span.startSeq - minSeq) / range) * 100;
            const widthPercent = Math.max(((span.endSeq - span.startSeq) / range) * 100, 1.5);
            const isHovered = hoveredIdx === idx;

            return (
              <div
                key={idx}
                className="flex items-center gap-2 group"
                onMouseEnter={() => setHoveredIdx(idx)}
                onMouseLeave={() => setHoveredIdx(null)}
              >
                {/* Row label */}
                <div className="w-20 shrink-0 text-right">
                  <span className="text-[10px] text-muted-foreground truncate block">{span.label}</span>
                </div>

                {/* Bar track */}
                <div className="flex-1 relative h-5 bg-muted/30 rounded-sm overflow-hidden">
                  <div
                    className={cn(
                      "absolute top-0.5 bottom-0.5 rounded-sm cursor-pointer transition-opacity",
                      isHovered ? "opacity-80" : "opacity-100",
                    )}
                    style={{
                      left: `${leftPercent}%`,
                      width: `${widthPercent}%`,
                      minWidth: 6,
                      backgroundColor: span.color,
                    }}
                    onClick={() => onEventClick?.(span.startSeq)}
                    title={`${span.tool}${span.summary ? ": " + span.summary : ""} (#${span.startSeq}${span.endSeq !== span.startSeq ? " → #" + span.endSeq : ""})`}
                  />
                </div>

                {/* Summary (shown on hover) */}
                <div className="w-48 shrink-0 overflow-hidden">
                  {isHovered && span.summary && (
                    <span className="text-[10px] text-muted-foreground truncate block">
                      {span.summary}
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>

        {/* X-axis */}
        <div className="flex items-center gap-2 mt-1">
          <div className="w-20 shrink-0" />
          <div className="flex-1 relative h-4">
            {ticks.map((tick) => {
              const pos = ((tick - minSeq) / range) * 100;
              return (
                <span
                  key={tick}
                  className="absolute text-[9px] text-muted-foreground/60 -translate-x-1/2"
                  style={{ left: `${pos}%` }}
                >
                  #{tick}
                </span>
              );
            })}
          </div>
          <div className="w-48 shrink-0" />
        </div>
      </div>
    </div>
  );
}

// ─── Token consumption curve (with input/output split + cost) ────────────────

// Token estimation: separate input (fed to model) vs output (generated by model)
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

// Rough cost estimate (Claude Sonnet 4 pricing)
const INPUT_COST_PER_TOKEN = 3 / 1_000_000; // $3/M input tokens
const OUTPUT_COST_PER_TOKEN = 15 / 1_000_000; // $15/M output tokens

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
  const { dataPoints, totalInput, totalOutput, totalCost, errorSeqs } = useMemo(() => {
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

    const cost = cumInput * INPUT_COST_PER_TOKEN + cumOutput * OUTPUT_COST_PER_TOKEN;

    return {
      dataPoints: points,
      totalInput: cumInput,
      totalOutput: cumOutput,
      totalCost: cost,
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
        <h4 className="text-xs font-medium text-muted-foreground">Token Consumption (estimated)</h4>
        <div className="flex items-center gap-3 text-[10px]">
          <span className="text-muted-foreground">
            Input: <span className="font-medium text-foreground">{formatTokens(totalInput)}</span>
          </span>
          <span className="text-muted-foreground">
            Output: <span className="font-medium text-foreground">{formatTokens(totalOutput)}</span>
          </span>
          <span className="text-muted-foreground">
            Total: <span className="font-medium text-foreground">{formatTokens(totalInput + totalOutput)}</span>
          </span>
          <span className="text-muted-foreground">
            Cost: <span className="font-medium text-foreground">~{formatCost(totalCost)}</span>
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
                  const eventCost = d.inputTokens * INPUT_COST_PER_TOKEN + d.outputTokens * OUTPUT_COST_PER_TOKEN;
                  return (
                    <div className="flex flex-col gap-0.5">
                      <span className="font-medium">#{d.seq} — {eventLabel}</span>
                      <span className="text-muted-foreground">
                        In: {formatTokens(d.inputTokens)} · Out: {formatTokens(d.outputTokens)}
                      </span>
                      <span className="text-muted-foreground">
                        Cumulative: {formatTokens(d.cumulativeTotal)} · ~{formatCost(eventCost)}
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
