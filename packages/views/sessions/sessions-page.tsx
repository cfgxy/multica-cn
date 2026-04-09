"use client";

import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bot,
  CheckCircle2,
  XCircle,
  Loader2,
  Clock,
  Ban,
  Zap,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { workspaceTasksOptions, workspaceKeys, agentListOptions } from "@multica/core/workspace/queries";
import { useWSEvent } from "@multica/core/realtime";
import { ActorAvatar } from "../common/actor-avatar";
import { AppLink } from "../navigation";
import type { AgentTask } from "@multica/core/types/agent";

// ─── Helpers ────────────────────────────────────────────────────────────────

function formatElapsed(startTime: string): string {
  const ms = Date.now() - new Date(startTime).getTime();
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const secs = seconds % 60;
  if (minutes < 60) return `${minutes}m ${secs}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
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

const statusConfig: Record<TaskStatus, { label: string; icon: typeof Loader2; className: string }> = {
  queued: { label: "Queued", icon: Clock, className: "text-muted-foreground" },
  dispatched: { label: "Dispatched", icon: Loader2, className: "text-info" },
  running: { label: "Running", icon: Loader2, className: "text-info" },
  completed: { label: "Completed", icon: CheckCircle2, className: "text-success" },
  failed: { label: "Failed", icon: XCircle, className: "text-destructive" },
  cancelled: { label: "Cancelled", icon: Ban, className: "text-muted-foreground" },
};

// ─── Sessions page ──────────────────────────────────────────────────────────

export function SessionsPage() {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const { data: tasks = [], isLoading } = useQuery(workspaceTasksOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  // Real-time: invalidate task list on task state changes
  const invalidate = useCallback(() => {
    qc.invalidateQueries({ queryKey: workspaceKeys.tasks(wsId) });
  }, [qc, wsId]);

  useWSEvent("task:dispatch", invalidate);
  useWSEvent("task:completed", invalidate);
  useWSEvent("task:failed", invalidate);
  useWSEvent("task:cancelled", invalidate);

  const getAgentName = (agentId: string) => {
    return agents.find((a) => a.id === agentId)?.name ?? "Agent";
  };

  // Separate active vs completed tasks
  const activeTasks = tasks.filter(
    (t) => t.status === "running" || t.status === "dispatched" || t.status === "queued",
  );
  const pastTasks = tasks.filter(
    (t) => t.status === "completed" || t.status === "failed" || t.status === "cancelled",
  );

  return (
    <div className="flex-1 overflow-y-auto">
      {/* Header */}
      <div className="border-b px-6 py-4">
        <div className="flex items-center gap-2">
          <Zap className="h-5 w-5 text-muted-foreground" />
          <h1 className="text-lg font-semibold">Sessions</h1>
          {activeTasks.length > 0 && (
            <span className="rounded-full bg-info/15 px-2 py-0.5 text-xs font-medium text-info">
              {activeTasks.length} active
            </span>
          )}
        </div>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : tasks.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 gap-3 text-muted-foreground">
          <Zap className="h-8 w-8" />
          <p className="text-sm">No task sessions yet.</p>
          <p className="text-xs">Sessions will appear here when agents start working on issues.</p>
        </div>
      ) : (
        <div className="divide-y">
          {/* Active sessions */}
          {activeTasks.length > 0 && (
            <div>
              <div className="px-6 py-2 bg-muted/30">
                <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                  Active
                </span>
              </div>
              {activeTasks.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  agentName={getAgentName(task.agent_id)}
                />
              ))}
            </div>
          )}

          {/* Past sessions */}
          {pastTasks.length > 0 && (
            <div>
              <div className="px-6 py-2 bg-muted/30">
                <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                  Recent
                </span>
              </div>
              {pastTasks.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  agentName={getAgentName(task.agent_id)}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ─── Task row ───────────────────────────────────────────────────────────────

function TaskRow({ task, agentName }: { task: AgentTask; agentName: string }) {
  const config = statusConfig[task.status];
  const StatusIcon = config.icon;
  const isActive = task.status === "running" || task.status === "dispatched";

  return (
    <AppLink href={`/issues/${task.issue_id}`}>
      <div className="flex items-center gap-3 px-6 py-3 hover:bg-accent/50 transition-colors cursor-pointer">
        {/* Agent avatar */}
        <ActorAvatar actorType="agent" actorId={task.agent_id} size={28} />

        {/* Main info */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium truncate">{agentName}</span>
            <span className="text-xs text-muted-foreground truncate">
              {task.issue_id.slice(0, 8)}
            </span>
          </div>
          {task.error && (
            <p className="text-xs text-destructive truncate mt-0.5">{task.error}</p>
          )}
        </div>

        {/* Status */}
        <div className={cn("flex items-center gap-1.5 shrink-0", config.className)}>
          <StatusIcon
            className={cn(
              "h-3.5 w-3.5",
              isActive && "animate-spin",
            )}
          />
          <span className="text-xs font-medium">{config.label}</span>
        </div>

        {/* Time */}
        <div className="text-xs text-muted-foreground shrink-0 w-20 text-right">
          {isActive && task.started_at
            ? formatElapsed(task.started_at)
            : task.completed_at
              ? formatTime(task.completed_at)
              : formatTime(task.created_at)}
        </div>
      </div>
    </AppLink>
  );
}
