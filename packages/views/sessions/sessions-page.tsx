"use client";

import { useState, useCallback, useEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2,
  XCircle,
  Loader2,
  Clock,
  Ban,
  Zap,
  Wrench,
  Hash,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { workspaceTasksOptions, workspaceKeys, agentListOptions } from "@multica/core/workspace/queries";
import { useWSEvent } from "@multica/core/realtime";
import { api } from "@multica/core/api";
import { ActorAvatar } from "../common/actor-avatar";
import { AgentTranscriptDialog } from "../issues/components";
import type { AgentTask, WorkspaceTask } from "@multica/core/types/agent";
import type { TaskMessagePayload } from "@multica/core/types/events";

// ─── Helpers ────────────────────────────────────────────────────────────────

function formatDuration(startIso: string, endIso?: string | null): string {
  const end = endIso ? new Date(endIso).getTime() : Date.now();
  const ms = end - new Date(startIso).getTime();
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const secs = seconds % 60;
  if (minutes < 60) return `${minutes}m ${secs}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

function formatTokens(n: number): string {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return String(n);
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

type TaskStatus = AgentTask["status"];

const statusConfig: Record<TaskStatus, { label: string; icon: typeof Loader2; className: string; dotClass: string }> = {
  queued: { label: "Queued", icon: Clock, className: "text-muted-foreground", dotClass: "bg-muted-foreground" },
  dispatched: { label: "Starting", icon: Loader2, className: "text-info", dotClass: "bg-info animate-pulse" },
  running: { label: "Running", icon: Loader2, className: "text-info", dotClass: "bg-info animate-pulse" },
  completed: { label: "Completed", icon: CheckCircle2, className: "text-success", dotClass: "bg-success" },
  failed: { label: "Failed", icon: XCircle, className: "text-destructive", dotClass: "bg-destructive" },
  cancelled: { label: "Cancelled", icon: Ban, className: "text-muted-foreground", dotClass: "bg-muted-foreground" },
};

interface TimelineItem {
  seq: number;
  type: "tool_use" | "tool_result" | "thinking" | "text" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
}

// ─── Sessions page ──────────────────────────────────────────────────────────

export function SessionsPage() {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const { data: tasks = [], isLoading } = useQuery(workspaceTasksOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  // Transcript dialog state
  const [selectedTask, setSelectedTask] = useState<AgentTask | null>(null);
  const [transcriptItems, setTranscriptItems] = useState<TimelineItem[]>([]);
  const [loadingTranscript, setLoadingTranscript] = useState(false);

  // Real-time: invalidate task list on task state changes
  const invalidate = useCallback(() => {
    qc.invalidateQueries({ queryKey: workspaceKeys.tasks(wsId) });
  }, [qc, wsId]);

  useWSEvent("task:dispatch", invalidate);
  useWSEvent("task:completed", invalidate);
  useWSEvent("task:failed", invalidate);
  useWSEvent("task:cancelled", invalidate);

  const getAgentName = useCallback(
    (agentId: string) => agents.find((a) => a.id === agentId)?.name ?? "Agent",
    [agents],
  );

  // Open transcript for a task
  const openTranscript = useCallback(
    async (task: AgentTask) => {
      setSelectedTask(task);
      setLoadingTranscript(true);
      try {
        const messages = await api.listTaskMessages(task.id);
        const items: TimelineItem[] = messages.map((m: TaskMessagePayload) => ({
          seq: m.seq,
          type: m.type,
          tool: m.tool,
          content: m.content,
          input: m.input,
          output: m.output,
        }));
        setTranscriptItems(items);
      } catch {
        setTranscriptItems([]);
      }
      setLoadingTranscript(false);
    },
    [],
  );

  // Live-update transcript items if the selected task is running
  const isSelectedLive = selectedTask && (selectedTask.status === "running" || selectedTask.status === "dispatched");

  useWSEvent(
    "task:message",
    useCallback(
      (payload: unknown) => {
        const p = payload as TaskMessagePayload;
        if (!selectedTask || p.task_id !== selectedTask.id) return;
        setTranscriptItems((prev) => [
          ...prev,
          { seq: p.seq, type: p.type, tool: p.tool, content: p.content, input: p.input, output: p.output },
        ]);
      },
      [selectedTask],
    ),
  );

  // Elapsed time ticker for active tasks
  const [, setTick] = useState(0);
  const hasActiveTasks = tasks.some((t) => t.status === "running" || t.status === "dispatched");
  useEffect(() => {
    if (!hasActiveTasks) return;
    const interval = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(interval);
  }, [hasActiveTasks]);

  // Sort: active first, then by created_at desc
  const sortedTasks = useMemo(() => {
    return [...tasks].sort((a, b) => {
      const aActive = ["running", "dispatched", "queued"].includes(a.status);
      const bActive = ["running", "dispatched", "queued"].includes(b.status);
      if (aActive !== bActive) return aActive ? -1 : 1;
      return new Date(b.created_at).getTime() - new Date(a.created_at).getTime();
    });
  }, [tasks]);

  const activeCount = tasks.filter((t) => t.status === "running" || t.status === "dispatched").length;

  return (
    <div className="flex-1 overflow-y-auto">
      {/* Header */}
      <div className="border-b px-6 py-4">
        <div className="flex items-center gap-2">
          <Zap className="h-5 w-5 text-muted-foreground" />
          <h1 className="text-lg font-semibold">Sessions</h1>
          {activeCount > 0 && (
            <span className="rounded-full bg-info/15 px-2 py-0.5 text-xs font-medium text-info">
              {activeCount} active
            </span>
          )}
        </div>
        <p className="text-xs text-muted-foreground mt-1">
          Agent execution sessions across this workspace
        </p>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : sortedTasks.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 gap-3 text-muted-foreground">
          <Zap className="h-8 w-8" />
          <p className="text-sm">No sessions yet</p>
          <p className="text-xs">Sessions appear when agents start working on issues.</p>
        </div>
      ) : (
        <div className="p-4 space-y-2">
          {sortedTasks.map((task) => (
            <SessionCard
              key={task.id}
              task={task}
              agentName={getAgentName(task.agent_id)}
              onClick={() => openTranscript(task)}
            />
          ))}
        </div>
      )}

      {/* Transcript dialog */}
      {selectedTask && (
        <AgentTranscriptDialog
          open={!!selectedTask}
          onOpenChange={(open) => {
            if (!open) {
              setSelectedTask(null);
              setTranscriptItems([]);
            }
          }}
          task={selectedTask}
          items={loadingTranscript ? [] : transcriptItems}
          agentName={getAgentName(selectedTask.agent_id)}
          isLive={!!isSelectedLive}
        />
      )}
    </div>
  );
}

// ─── Session card ───────────────────────────────────────────────────────────

function SessionCard({
  task,
  agentName,
  onClick,
}: {
  task: WorkspaceTask;
  agentName: string;
  onClick: () => void;
}) {
  const config = statusConfig[task.status];
  const isActive = task.status === "running" || task.status === "dispatched";
  const totalTokens = task.total_input_tokens + task.total_output_tokens;

  return (
    <button
      onClick={onClick}
      className={cn(
        "w-full rounded-lg border p-3 text-left transition-all hover:shadow-sm hover:border-border/80",
        isActive
          ? "border-info/30 bg-info/5 hover:bg-info/8"
          : "hover:bg-accent/30",
      )}
    >
      {/* Top row: agent + status */}
      <div className="flex items-center gap-2.5">
        <ActorAvatar actorType="agent" actorId={task.agent_id} size={24} />
        <span className="text-xs font-medium text-muted-foreground">{agentName}</span>
        <div className="ml-auto flex items-center gap-1.5">
          <span className={cn("h-1.5 w-1.5 rounded-full shrink-0", config.dotClass)} />
          <span className={cn("text-[11px]", config.className)}>{config.label}</span>
        </div>
      </div>

      {/* Issue title */}
      <div className="mt-2">
        {task.issue_title ? (
          <p className="text-sm font-medium truncate">
            {task.issue_number > 0 && (
              <span className="text-muted-foreground font-normal mr-1">#{task.issue_number}</span>
            )}
            {task.issue_title}
          </p>
        ) : (
          <p className="text-sm text-muted-foreground truncate">{task.issue_id.slice(0, 8)}</p>
        )}
        {task.error && (
          <p className="text-xs text-destructive truncate mt-0.5">{task.error}</p>
        )}
      </div>

      {/* Metadata chips */}
      <div className="flex items-center gap-3 mt-2 text-[11px] text-muted-foreground">
        {/* Duration */}
        <span className="flex items-center gap-1">
          <Clock className="h-3 w-3" />
          {isActive && task.started_at
            ? formatDuration(task.started_at)
            : task.started_at && task.completed_at
              ? formatDuration(task.started_at, task.completed_at)
              : "—"}
        </span>

        {/* Tool calls */}
        {task.tool_use_count > 0 && (
          <span className="flex items-center gap-1">
            <Wrench className="h-3 w-3" />
            {task.tool_use_count} tools
          </span>
        )}

        {/* Events */}
        {task.total_events > 0 && (
          <span className="flex items-center gap-1">
            <Hash className="h-3 w-3" />
            {task.total_events} events
          </span>
        )}

        {/* Tokens */}
        {totalTokens > 0 && (
          <span className="flex items-center gap-1">
            <Zap className="h-3 w-3" />
            {formatTokens(totalTokens)} tokens
          </span>
        )}

        {/* Time */}
        <span className="ml-auto">
          {formatTime(task.created_at)}
        </span>
      </div>
    </button>
  );
}
