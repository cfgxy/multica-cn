"use client";

import { useState, useCallback, useEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bot,
  CheckCircle2,
  XCircle,
  Loader2,
  Clock,
  Ban,
  Zap,
  ExternalLink,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { workspaceTasksOptions, workspaceKeys, agentListOptions } from "@multica/core/workspace/queries";
import { issueListOptions } from "@multica/core/issues/queries";
import { useWSEvent } from "@multica/core/realtime";
import { api } from "@multica/core/api";
import { ActorAvatar } from "../common/actor-avatar";
import { AgentTranscriptDialog } from "../issues/components";
import type { AgentTask } from "@multica/core/types/agent";
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

function formatRelativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
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

// ─── Timeline item type (matching transcript dialog) ────────────────────────

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
  const { data: issues = [] } = useQuery(issueListOptions(wsId));

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

  // Lookup helpers
  const getAgentName = useCallback(
    (agentId: string) => agents.find((a) => a.id === agentId)?.name ?? "Agent",
    [agents],
  );
  const getIssue = useCallback(
    (issueId: string) => issues.find((issue) => issue.id === issueId),
    [issues],
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
          {
            seq: p.seq,
            type: p.type,
            tool: p.tool,
            content: p.content,
            input: p.input,
            output: p.output,
          },
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

  return (
    <div className="flex-1 overflow-y-auto">
      {/* Header */}
      <div className="border-b px-6 py-4">
        <div className="flex items-center gap-2">
          <Zap className="h-5 w-5 text-muted-foreground" />
          <h1 className="text-lg font-semibold">Sessions</h1>
          {hasActiveTasks && (
            <span className="rounded-full bg-info/15 px-2 py-0.5 text-xs font-medium text-info">
              {tasks.filter((t) => t.status === "running" || t.status === "dispatched").length} active
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
        <div className="px-4 py-3">
          {/* Table header */}
          <div className="grid grid-cols-[auto_1fr_auto_auto] gap-3 px-3 py-1.5 text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
            <div className="w-7" />
            <div>Session</div>
            <div className="w-20 text-center">Status</div>
            <div className="w-24 text-right">Duration</div>
          </div>

          {/* Task rows */}
          <div className="space-y-1">
            {sortedTasks.map((task) => {
              const isActive = task.status === "running" || task.status === "dispatched" || task.status === "queued";
              const issue = getIssue(task.issue_id);
              const agentName = getAgentName(task.agent_id);
              const config = statusConfig[task.status];

              return (
                <button
                  key={task.id}
                  onClick={() => openTranscript(task)}
                  className={cn(
                    "w-full grid grid-cols-[auto_1fr_auto_auto] gap-3 items-center px-3 py-2.5 rounded-lg text-left transition-colors",
                    "hover:bg-accent/50 cursor-pointer",
                    isActive && "bg-accent/20",
                  )}
                >
                  {/* Agent avatar */}
                  <ActorAvatar actorType="agent" actorId={task.agent_id} size={28} />

                  {/* Session info */}
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      {issue ? (
                        <span className="text-sm font-medium truncate">
                          <span className="text-muted-foreground font-normal">{issue.identifier}</span>
                          {" "}
                          {issue.title}
                        </span>
                      ) : (
                        <span className="text-sm font-medium truncate text-muted-foreground">
                          {task.issue_id.slice(0, 8)}
                        </span>
                      )}
                    </div>
                    <div className="flex items-center gap-2 mt-0.5">
                      <span className="text-xs text-muted-foreground">{agentName}</span>
                      {task.error && (
                        <>
                          <span className="text-muted-foreground/30">·</span>
                          <span className="text-xs text-destructive truncate">{task.error}</span>
                        </>
                      )}
                    </div>
                  </div>

                  {/* Status */}
                  <div className={cn("flex items-center gap-1.5 w-20 justify-center", config.className)}>
                    <span className={cn("h-1.5 w-1.5 rounded-full shrink-0", config.dotClass)} />
                    <span className="text-xs">{config.label}</span>
                  </div>

                  {/* Duration / time */}
                  <div className="text-xs text-muted-foreground w-24 text-right tabular-nums">
                    {isActive && task.started_at ? (
                      formatDuration(task.started_at)
                    ) : task.started_at && task.completed_at ? (
                      formatDuration(task.started_at, task.completed_at)
                    ) : (
                      formatRelativeTime(task.created_at)
                    )}
                  </div>
                </button>
              );
            })}
          </div>
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
