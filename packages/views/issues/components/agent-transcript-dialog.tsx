"use client";

import { useState, useRef, useCallback, useEffect } from "react";
import {
  Bot,
  ChevronRight,
  Brain,
  AlertCircle,
  CheckCircle2,
  XCircle,
  X,
  Loader2,
  Clock,
  Copy,
  Check,
  Monitor,
  Cloud,
  Cpu,
  FileCode,
  Search,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Dialog, DialogContent, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@multica/ui/components/ui/collapsible";
import { ActorAvatar } from "../../common/actor-avatar";
import { Markdown } from "../../common/markdown";
import { api } from "@multica/core/api";
import type { AgentTask, Agent, AgentRuntime } from "@multica/core/types/agent";
import { redactSecrets } from "../utils/redact";

// ─── Types ──────────────────────────────────────────────────────────────────

interface TimelineItem {
  seq: number;
  type: "tool_use" | "tool_result" | "thinking" | "text" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
}

interface AgentTranscriptDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  task: AgentTask;
  items: TimelineItem[];
  agentName: string;
  isLive?: boolean;
}

// ─── Color mapping for timeline segments ────────────────────────────────────

type EventColor = "agent" | "thinking" | "tool" | "result" | "error";

function getEventColor(item: TimelineItem): EventColor {
  switch (item.type) {
    case "text":
      return "agent";
    case "thinking":
      return "thinking";
    case "tool_use":
      return "tool";
    case "tool_result":
      return "result";
    case "error":
      return "error";
    default:
      return "result";
  }
}

const colorClasses: Record<EventColor, { bg: string; bgActive: string; label: string }> = {
  agent: { bg: "bg-emerald-400/60", bgActive: "bg-emerald-500", label: "bg-emerald-500" },
  thinking: { bg: "bg-violet-400/60", bgActive: "bg-violet-500", label: "bg-violet-500/20 text-violet-700 dark:text-violet-300" },
  tool: { bg: "bg-blue-400/60", bgActive: "bg-blue-500", label: "bg-blue-500/20 text-blue-700 dark:text-blue-300" },
  result: { bg: "bg-slate-300/60 dark:bg-slate-600/60", bgActive: "bg-slate-400 dark:bg-slate-500", label: "bg-muted text-muted-foreground" },
  error: { bg: "bg-red-400/60", bgActive: "bg-red-500", label: "bg-red-500/20 text-red-700 dark:text-red-300" },
};

// ─── Helpers ────────────────────────────────────────────────────────────────

function getEventLabel(item: TimelineItem): string {
  switch (item.type) {
    case "text":
      return "Agent";
    case "thinking":
      return "Thinking";
    case "tool_use":
      return item.tool ?? "Tool";
    case "tool_result":
      return item.tool ? `${item.tool}` : "Result";
    case "error":
      return "Error";
    default:
      return "Event";
  }
}

function getEventSummary(item: TimelineItem): string {
  switch (item.type) {
    case "text":
      return item.content?.split("\n").filter(Boolean).pop() ?? "";
    case "thinking":
      return item.content?.slice(0, 200) ?? "";
    case "tool_use": {
      if (!item.input) return "";
      const inp = item.input as Record<string, string>;
      if (inp.query) return inp.query;
      if (inp.file_path) return shortenPath(inp.file_path);
      if (inp.path) return shortenPath(inp.path);
      if (inp.pattern) return inp.pattern;
      if (inp.description) return String(inp.description);
      if (inp.command) {
        const cmd = String(inp.command);
        return cmd.length > 120 ? cmd.slice(0, 120) + "..." : cmd;
      }
      if (inp.prompt) {
        const p = String(inp.prompt);
        return p.length > 120 ? p.slice(0, 120) + "..." : p;
      }
      if (inp.skill) return String(inp.skill);
      for (const v of Object.values(inp)) {
        if (typeof v === "string" && v.length > 0 && v.length < 120) return v;
      }
      return "";
    }
    case "tool_result":
      return item.output?.slice(0, 200) ?? "";
    case "error":
      return item.content ?? "";
    default:
      return "";
  }
}

function shortenPath(p: string): string {
  const parts = p.split("/");
  if (parts.length <= 3) return p;
  return ".../" + parts.slice(-2).join("/");
}

function formatDuration(start: string, end: string): string {
  const ms = new Date(end).getTime() - new Date(start).getTime();
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const secs = seconds % 60;
  return `${minutes}m ${secs}s`;
}

function formatElapsedMs(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const secs = seconds % 60;
  return `${minutes}m ${secs}s`;
}

// ─── Main dialog ────────────────────────────────────────────────────────────

export function AgentTranscriptDialog({
  open,
  onOpenChange,
  task,
  items,
  agentName,
  isLive = false,
}: AgentTranscriptDialogProps) {
  const [selectedIdx, setSelectedIdx] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState("");
  const [copied, setCopied] = useState(false);
  const [agentInfo, setAgentInfo] = useState<Agent | null>(null);
  const [runtimeInfo, setRuntimeInfo] = useState<AgentRuntime | null>(null);
  const eventRefs = useRef<Map<number, HTMLDivElement>>(new Map());
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  // Fetch agent and runtime metadata when dialog opens
  useEffect(() => {
    if (!open) return;
    let cancelled = false;

    if (task.agent_id) {
      api.getAgent(task.agent_id).then((agent) => {
        if (!cancelled) setAgentInfo(agent);
      }).catch(() => {});
    }

    if (task.runtime_id) {
      api.listRuntimes().then((runtimes) => {
        if (cancelled) return;
        const rt = runtimes.find((r) => r.id === task.runtime_id);
        if (rt) setRuntimeInfo(rt);
      }).catch(() => {});
    }

    return () => { cancelled = true; };
  }, [open, task.agent_id, task.runtime_id]);

  // Elapsed time for live tasks
  useEffect(() => {
    if (!isLive || (!task.started_at && !task.dispatched_at)) return;
    const startRef = task.started_at ?? task.dispatched_at!;
    const update = () => setElapsed(formatElapsedMs(Date.now() - new Date(startRef).getTime()));
    update();
    const interval = setInterval(update, 1000);
    return () => clearInterval(interval);
  }, [isLive, task.started_at, task.dispatched_at]);

  // Click a timeline segment → scroll to event and select it
  const handleSegmentClick = useCallback((idx: number) => {
    setSelectedIdx(idx);
    const el = eventRefs.current.get(idx);
    if (el) {
      el.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  }, []);

  // Copy all events as text
  const handleCopyAll = useCallback(() => {
    const text = items
      .map((item) => {
        const label = getEventLabel(item);
        const summary = getEventSummary(item);
        return `[${label}] ${summary}`;
      })
      .join("\n");
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [items]);

  // Duration
  const duration =
    task.started_at && task.completed_at
      ? formatDuration(task.started_at, task.completed_at)
      : isLive
        ? elapsed
        : null;

  const toolCount = items.filter((i) => i.type === "tool_use").length;

  // Status display
  const statusBadge = isLive ? (
    <span className="inline-flex items-center gap-1 rounded-full bg-info/15 px-2 py-0.5 text-xs font-medium text-info">
      <Loader2 className="h-3 w-3 animate-spin" />
      Running
    </span>
  ) : task.status === "completed" ? (
    <span className="inline-flex items-center gap-1 rounded-full bg-success/15 px-2 py-0.5 text-xs font-medium text-success">
      <CheckCircle2 className="h-3 w-3" />
      Completed
    </span>
  ) : task.status === "failed" ? (
    <span className="inline-flex items-center gap-1 rounded-full bg-destructive/15 px-2 py-0.5 text-xs font-medium text-destructive">
      <XCircle className="h-3 w-3" />
      Failed
    </span>
  ) : (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground capitalize">
      {task.status}
    </span>
  );

  // Resolve selected item safely
  const selectedItem = selectedIdx !== null && selectedIdx < items.length ? items[selectedIdx]! : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="!max-w-[85vw] !w-[calc(100vw-4rem)] !max-h-[calc(100vh-4rem)] !h-[calc(100vh-4rem)] flex flex-col !p-0 !gap-0 overflow-hidden"
        showCloseButton={false}
      >
        <DialogTitle className="sr-only">Agent Execution Transcript</DialogTitle>

        {/* ── Header ─────────────────────────────────────────────── */}
        <div className="border-b px-4 py-3 shrink-0 space-y-2">
          {/* Top row: agent name, status, actions */}
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-2">
              {task.agent_id ? (
                <ActorAvatar actorType="agent" actorId={task.agent_id} size={24} />
              ) : (
                <div className="flex items-center justify-center h-6 w-6 rounded-full bg-info/10 text-info">
                  <Bot className="h-3.5 w-3.5" />
                </div>
              )}
              <span className="font-medium text-sm">{agentName}</span>
            </div>

            {statusBadge}

            <div className="ml-auto flex items-center gap-1">
              <button
                onClick={handleCopyAll}
                className="flex items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
                {copied ? "Copied" : "Copy all"}
              </button>
              <button
                onClick={() => onOpenChange(false)}
                className="flex items-center justify-center rounded p-1 text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
          </div>

          {/* Metadata chips row */}
          <div className="flex items-center gap-2 flex-wrap text-xs">
            {/* Runtime provider */}
            {runtimeInfo?.provider && (
              <MetadataChip icon={<Cpu className="h-3 w-3" />}>
                {formatProvider(runtimeInfo.provider)}
              </MetadataChip>
            )}

            {/* Runtime environment */}
            {runtimeInfo && (
              <MetadataChip
                icon={runtimeInfo.runtime_mode === "cloud" ? <Cloud className="h-3 w-3" /> : <Monitor className="h-3 w-3" />}
              >
                {runtimeInfo.name}
                <span className="text-muted-foreground/60 ml-0.5">({runtimeInfo.runtime_mode})</span>
              </MetadataChip>
            )}

            {/* Agent type / description */}
            {agentInfo?.description && (
              <MetadataChip icon={<Bot className="h-3 w-3" />}>
                {agentInfo.description.length > 40 ? agentInfo.description.slice(0, 40) + "..." : agentInfo.description}
              </MetadataChip>
            )}

            {/* Duration */}
            {duration && (
              <MetadataChip icon={<Clock className="h-3 w-3" />}>
                {duration}
              </MetadataChip>
            )}

            {/* Event counts */}
            {toolCount > 0 && (
              <MetadataChip>{toolCount} tool calls</MetadataChip>
            )}
            <MetadataChip>{items.length} events</MetadataChip>

            {/* Created time */}
            {task.created_at && (
              <MetadataChip>
                {new Date(task.created_at).toLocaleString(undefined, {
                  month: "short",
                  day: "numeric",
                  hour: "2-digit",
                  minute: "2-digit",
                })}
              </MetadataChip>
            )}
          </div>
        </div>

        {/* ── Timeline progress bar ─────────────────────────────── */}
        {items.length > 0 && (
          <div className="border-b px-4 py-2.5 shrink-0">
            <TimelineBar
              items={items}
              selectedIdx={selectedIdx}
              onSegmentClick={handleSegmentClick}
            />
          </div>
        )}

        {/* ── Split content: event list + detail panel ────────── */}
        <div className="flex-1 flex min-h-0">
          {/* Left: Event list */}
          <div
            ref={scrollContainerRef}
            className={cn(
              "overflow-y-auto min-h-0 transition-[width] duration-200",
              selectedItem ? "w-[40%] border-r" : "w-full",
            )}
          >
            {items.length === 0 ? (
              <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
                {isLive ? (
                  <div className="flex items-center gap-2">
                    <Loader2 className="h-4 w-4 animate-spin" />
                    Waiting for events...
                  </div>
                ) : (
                  "No execution data recorded."
                )}
              </div>
            ) : (
              <div className="divide-y">
                {items.map((item, idx) => (
                  <TranscriptEventRow
                    key={`${item.seq}-${idx}`}
                    ref={(el) => {
                      if (el) eventRefs.current.set(idx, el);
                      else eventRefs.current.delete(idx);
                    }}
                    item={item}
                    index={idx}
                    isSelected={selectedIdx === idx}
                    onClick={() => setSelectedIdx(idx === selectedIdx ? null : idx)}
                  />
                ))}
              </div>
            )}
          </div>

          {/* Right: Detail panel */}
          {selectedItem && (
            <DetailPanel
              item={selectedItem}
              onClose={() => setSelectedIdx(null)}
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

// ─── Metadata chip ──────────────────────────────────────────────────────────

function MetadataChip({ icon, children }: { icon?: React.ReactNode; children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md border bg-muted/50 px-2 py-0.5 text-[11px] text-muted-foreground">
      {icon}
      {children}
    </span>
  );
}

function formatProvider(provider: string): string {
  const map: Record<string, string> = {
    claude: "Claude Code",
    "claude-code": "Claude Code",
    codex: "Codex",
  };
  return map[provider.toLowerCase()] ?? provider;
}

// ─── Timeline bar (colored segments) ────────────────────────────────────────

function TimelineBar({
  items,
  selectedIdx,
  onSegmentClick,
}: {
  items: TimelineItem[];
  selectedIdx: number | null;
  onSegmentClick: (idx: number) => void;
}) {
  // Group consecutive items of the same color into segments for cleaner display
  const segments: { startIdx: number; endIdx: number; color: EventColor; count: number }[] = [];
  let currentColor: EventColor | null = null;
  let currentStart = 0;

  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;
    const color = getEventColor(item);
    if (color !== currentColor) {
      if (currentColor !== null) {
        segments.push({ startIdx: currentStart, endIdx: i - 1, color: currentColor, count: i - currentStart });
      }
      currentColor = color;
      currentStart = i;
    }
  }
  if (currentColor !== null) {
    segments.push({ startIdx: currentStart, endIdx: items.length - 1, color: currentColor, count: items.length - currentStart });
  }

  return (
    <div className="flex gap-0.5 h-5 rounded overflow-hidden" role="navigation" aria-label="Timeline">
      {segments.map((seg, segIdx) => {
        const isSelected = selectedIdx !== null && selectedIdx >= seg.startIdx && selectedIdx <= seg.endIdx;
        const color = colorClasses[seg.color];
        // Width proportional to number of events in segment
        const widthPercent = (seg.count / items.length) * 100;

        return (
          <button
            key={segIdx}
            className={cn(
              "h-full transition-all duration-150 hover:opacity-80 relative group",
              isSelected ? color.bgActive : color.bg,
              "min-w-[4px]",
            )}
            style={{ width: `${Math.max(widthPercent, 0.5)}%` }}
            onClick={() => onSegmentClick(seg.startIdx)}
            title={`${getEventLabel(items[seg.startIdx]!)}${seg.count > 1 ? ` (+${seg.count - 1} more)` : ""}`}
          >
            {/* Tooltip on hover */}
            <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 hidden group-hover:block z-10 pointer-events-none">
              <div className="rounded bg-popover border px-2 py-1 text-[10px] text-popover-foreground shadow-md whitespace-nowrap">
                {getEventLabel(items[seg.startIdx]!)}
                {seg.count > 1 && <span className="text-muted-foreground ml-1">+{seg.count - 1}</span>}
              </div>
            </div>
          </button>
        );
      })}
    </div>
  );
}

// ─── Transcript event row (click to select → shows in detail panel) ──────

interface TranscriptEventRowProps {
  item: TimelineItem;
  index: number;
  isSelected: boolean;
  onClick: () => void;
}

const TranscriptEventRow = ({
  ref,
  item,
  isSelected,
  onClick,
}: TranscriptEventRowProps & { ref?: React.Ref<HTMLDivElement> }) => {
  const color = getEventColor(item);
  const label = getEventLabel(item);
  const summary = getEventSummary(item);

  return (
    <div
      ref={ref}
      className={cn(
        "group transition-colors cursor-pointer hover:bg-accent/30",
        isSelected && "bg-accent/50",
      )}
      onClick={onClick}
    >
      <div className="flex items-start gap-2 px-4 py-2">
        {/* Type label badge */}
        <span
          className={cn(
            "inline-flex items-center shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium mt-0.5 min-w-[60px] justify-center",
            colorClasses[color].label,
          )}
        >
          {item.type === "thinking" && <Brain className="h-3 w-3 mr-1 shrink-0" />}
          {item.type === "error" && <AlertCircle className="h-3 w-3 mr-1 shrink-0" />}
          {label}
        </span>

        {/* Summary */}
        <span
          className={cn(
            "flex-1 text-xs min-w-0 py-0.5 truncate",
            item.type === "error" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {summary || "(empty)"}
        </span>

        {/* Seq number */}
        <span className="shrink-0 text-[10px] text-muted-foreground/50 tabular-nums mt-1">
          #{item.seq}
        </span>
      </div>
    </div>
  );
};

// ─── Detail panel (sidebar) ──────────────────────────────────────────────

function DetailPanel({ item, onClose }: { item: TimelineItem; onClose: () => void }) {
  const [copied, setCopied] = useState(false);

  const rawContent =
    item.type === "tool_use"
      ? item.input
        ? redactSecrets(JSON.stringify(item.input, null, 2))
        : ""
      : item.type === "tool_result"
        ? redactSecrets(item.output ?? "")
        : redactSecrets(item.content ?? "");

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(rawContent).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [rawContent]);

  const color = getEventColor(item);

  return (
    <div className="w-[60%] flex flex-col min-h-0">
      {/* Header */}
      <div className="flex items-center gap-2 px-4 py-2.5 border-b shrink-0">
        <span
          className={cn(
            "inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium",
            colorClasses[color].label,
          )}
        >
          {item.type === "thinking" && <Brain className="h-3 w-3 mr-1" />}
          {item.type === "error" && <AlertCircle className="h-3 w-3 mr-1" />}
          {getEventLabel(item)}
        </span>
        {item.type === "tool_result" && (
          <span className="text-xs text-muted-foreground">result</span>
        )}
        <span className="text-[10px] text-muted-foreground/50">#{item.seq}</span>

        <div className="ml-auto flex items-center gap-1">
          <button
            onClick={handleCopy}
            className="flex items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
            {copied ? "Copied" : "Copy"}
          </button>
          <button
            onClick={onClose}
            className="flex items-center justify-center rounded p-1 text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-4">
        <DetailContent item={item} />
      </div>
    </div>
  );
}

// ─── Rich content rendering ──────────────────────────────────────────────

function DetailContent({ item }: { item: TimelineItem }) {
  switch (item.type) {
    case "tool_use":
      return <ToolUseDetail item={item} />;
    case "tool_result":
      return <ToolResultDetail item={item} />;
    case "text":
      return (
        <div className="prose prose-sm dark:prose-invert max-w-none">
          <Markdown mode="minimal">{item.content ?? ""}</Markdown>
        </div>
      );
    case "thinking":
      return (
        <div className="text-sm text-muted-foreground leading-relaxed whitespace-pre-wrap">
          {item.content ?? ""}
        </div>
      );
    case "error":
      return (
        <pre className="text-sm text-destructive whitespace-pre-wrap break-words font-mono">
          {item.content ?? ""}
        </pre>
      );
    default:
      return null;
  }
}

function ToolUseDetail({ item }: { item: TimelineItem }) {
  const [showRaw, setShowRaw] = useState(false);
  if (!item.input) return null;
  const inp = item.input;

  const command = typeof inp.command === "string" ? inp.command : undefined;
  const description = typeof inp.description === "string" ? inp.description : undefined;
  const filePath = typeof inp.file_path === "string" ? inp.file_path : typeof inp.path === "string" ? inp.path : undefined;
  const oldString = typeof inp.old_string === "string" ? inp.old_string : undefined;
  const newString = typeof inp.new_string === "string" ? inp.new_string : undefined;
  const content = typeof inp.content === "string" ? inp.content : undefined;
  const query = typeof inp.query === "string" ? inp.query : undefined;
  const pattern = typeof inp.pattern === "string" ? inp.pattern : undefined;
  const prompt = typeof inp.prompt === "string" ? inp.prompt : undefined;
  const skill = typeof inp.skill === "string" ? inp.skill : undefined;

  const hasStructuredView = !!(command || filePath || query || pattern || prompt || skill);

  return (
    <div className="space-y-3 text-sm">
      {/* Bash command */}
      {command && (
        <div className="space-y-1.5">
          {description && (
            <p className="text-xs text-muted-foreground">{description}</p>
          )}
          <Markdown mode="minimal">{`\`\`\`bash\n${redactSecrets(command)}\n\`\`\``}</Markdown>
        </div>
      )}

      {/* File path */}
      {filePath && (
        <div className="flex items-center gap-1.5">
          <FileCode className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
          <code className="bg-muted px-1.5 py-0.5 rounded text-xs break-all">{filePath}</code>
        </div>
      )}

      {/* Edit: old → new diff */}
      {oldString !== undefined && newString !== undefined && (
        <div className="rounded border overflow-hidden">
          <div className="bg-red-500/5 border-b px-3 py-2">
            <div className="text-[11px] font-medium text-red-600 dark:text-red-400 mb-1">Removed</div>
            <pre className="text-xs whitespace-pre-wrap break-all text-muted-foreground">{redactSecrets(oldString)}</pre>
          </div>
          <div className="bg-green-500/5 px-3 py-2">
            <div className="text-[11px] font-medium text-green-600 dark:text-green-400 mb-1">Added</div>
            <pre className="text-xs whitespace-pre-wrap break-all text-muted-foreground">{redactSecrets(newString)}</pre>
          </div>
        </div>
      )}

      {/* File content (Write tool) */}
      {content && !command && oldString === undefined && (
        <Markdown mode="minimal">
          {`\`\`\`\n${redactSecrets(content.length > 10000 ? content.slice(0, 10000) + "\n... (truncated)" : content)}\n\`\`\``}
        </Markdown>
      )}

      {/* Search query */}
      {query && (
        <div className="flex items-center gap-2">
          <Search className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
          <span className="font-medium">{query}</span>
        </div>
      )}

      {/* Glob/Grep pattern */}
      {pattern && !query && (
        <div className="flex items-center gap-2">
          <Search className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
          <code className="bg-muted px-1.5 py-0.5 rounded text-xs">{pattern}</code>
        </div>
      )}

      {/* Agent prompt */}
      {prompt && (
        <div className="prose prose-sm dark:prose-invert max-w-none">
          <Markdown mode="minimal">{redactSecrets(prompt)}</Markdown>
        </div>
      )}

      {/* Skill */}
      {skill && (
        <code className="bg-muted px-2 py-1 rounded text-xs">/{skill}</code>
      )}

      {/* Full JSON: inline when no structured view, collapsible toggle otherwise */}
      {!hasStructuredView ? (
        <Markdown mode="minimal">
          {`\`\`\`json\n${redactSecrets(JSON.stringify(inp, null, 2))}\n\`\`\``}
        </Markdown>
      ) : (
        <Collapsible open={showRaw} onOpenChange={setShowRaw}>
          <CollapsibleTrigger className="flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground transition-colors">
            <ChevronRight className={cn("h-3 w-3 transition-transform", showRaw && "rotate-90")} />
            Raw input
          </CollapsibleTrigger>
          <CollapsibleContent>
            <pre className="mt-1.5 max-h-60 overflow-auto rounded bg-muted/50 border p-3 text-[11px] text-muted-foreground whitespace-pre-wrap break-all">
              {redactSecrets(JSON.stringify(inp, null, 2))}
            </pre>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  );
}

function ToolResultDetail({ item }: { item: TimelineItem }) {
  const output = item.output ?? "";
  if (!output) {
    return <span className="text-sm text-muted-foreground italic">No output</span>;
  }

  const redacted = redactSecrets(output);
  const truncated = redacted.length > 20000;
  const displayContent = truncated ? redacted.slice(0, 20000) : redacted;

  // Try to detect and format JSON
  const trimmed = displayContent.trim();
  if (
    (trimmed.startsWith("{") && trimmed.endsWith("}")) ||
    (trimmed.startsWith("[") && trimmed.endsWith("]"))
  ) {
    try {
      const parsed = JSON.parse(trimmed);
      const formatted = JSON.stringify(parsed, null, 2);
      return (
        <div>
          <Markdown mode="minimal">
            {`\`\`\`json\n${formatted}\n\`\`\``}
          </Markdown>
          {truncated && (
            <p className="text-xs text-muted-foreground mt-2">... (truncated)</p>
          )}
        </div>
      );
    } catch {
      // Not valid JSON, fall through
    }
  }

  // Default: preformatted text with good readability
  return (
    <pre className="text-xs leading-relaxed text-muted-foreground whitespace-pre-wrap break-words font-mono">
      {displayContent}
      {truncated && "\n\n... (truncated)"}
    </pre>
  );
}
