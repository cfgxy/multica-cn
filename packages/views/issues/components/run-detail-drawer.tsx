"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { issueKeys } from "@multica/core/issues/queries";
import type { AgentTask, RunLineageEntry } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { useTimeAgo, useT } from "../../i18n";
import { formatDuration } from "../../agents/components/agent-activity-hover-content";
import {
  cancelReasonLabel,
  failureReasonLabel,
} from "../../agents/components/tabs/task-failure";
import { ActorAvatar } from "../../common/actor-avatar";
import { formatTokens, formatUsd, summarizeTaskUsage } from "../../runtimes/utils";
import { useStatusLabel, useTriggerText } from "./task-run-labels";
import { TaskStatusIcon } from "./task-status-icon";

// RUYI-292 run detail: the drawer behind clicking a past run in the
// execution log. One `getIssueRun` call returns the run plus its full retry
// chain (both lineage columns), so cancelled→retried→completed history, the
// human-readable failure reason beside its raw diagnostic, and the cancel
// attribution (who asked to stop, when) render without a second round trip.
//
// The query key sits under `issueKeys.tasks` so the global realtime
// `task:`-prefix invalidation refreshes an open drawer too — a stop that
// confirms while the user is reading the chain updates in place.

interface RunDetailDrawerProps {
  issueId: string;
  /** The clicked run; `null` keeps the drawer closed. */
  task: AgentTask | null;
  onOpenChange: (open: boolean) => void;
}

export function RunDetailDrawer({ issueId, task, onOpenChange }: RunDetailDrawerProps) {
  const { data } = useQuery({
    // Child of the list key: the realtime `task:` invalidation covers it by
    // prefix, and no cache entry lingers after the drawer closes for good.
    queryKey: [...issueKeys.tasks(issueId), "detail", task?.id ?? ""],
    queryFn: () => api.getIssueRun(issueId, task!.id),
    enabled: !!task,
    staleTime: 30_000,
  });

  const run = data?.task ?? task;

  return (
    <Sheet open={!!task} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex w-[360px] flex-col gap-4 overflow-y-auto p-4 sm:max-w-[400px]"
      >
        {run && (
          <RunDetailContent
            issueId={issueId}
            task={run}
            ancestors={data?.ancestors ?? []}
            descendants={data?.descendants ?? []}
          />
        )}
      </SheetContent>
    </Sheet>
  );
}

function RunDetailContent({
  task,
  ancestors,
  descendants,
}: {
  issueId: string;
  task: AgentTask;
  ancestors: RunLineageEntry[];
  descendants: RunLineageEntry[];
}) {
  const { t } = useT("issues");
  const { t: tAgents } = useT("agents");
  const timeAgo = useTimeAgo();
  const { getMemberName, getAgentName } = useActorName();
  const statusLabel = useStatusLabel(task.status);
  const trigger = useTriggerText(task);
  const usage = summarizeTaskUsage(task.usage);

  const failure = failureReasonLabel(task.failure_reason, tAgents);
  // A user-initiated cancel has no persisted reason — the attribution line
  // below is its "why". A server-side cancel (worktree gate, preserved-work
  // delivery) explains itself here instead.
  const cancelReason = cancelReasonLabel(task, tAgents);
  const duration =
    task.started_at && task.completed_at
      ? formatDuration(task.started_at, new Date(task.completed_at).getTime())
      : null;

  return (
    <>
      <SheetHeader className="p-0">
        <SheetTitle className="text-left text-body leading-snug">{trigger}</SheetTitle>
        <div className="flex items-center gap-1.5 text-caption">
          <TaskStatusIcon status={task.status} />
          <span>{statusLabel}</span>
        </div>
      </SheetHeader>

      <MetaRow label={t(($) => $.run_detail.agent)}>
        <span className="flex items-center gap-1.5">
          {task.agent_id && (
            <ActorAvatar actorType="agent" actorId={task.agent_id} size="xs" />
          )}
          <span>{task.agent_id ? getAgentName(task.agent_id) : "—"}</span>
        </span>
      </MetaRow>
      {(task.attempt ?? 1) > 1 && (
        <MetaRow label={t(($) => $.run_detail.attempt)}>
          {t(($) => $.run_detail.attempt_value, { attempt: task.attempt ?? 1 })}
        </MetaRow>
      )}
      <MetaRow label={t(($) => $.run_detail.created)}>
        {task.created_at ? timeAgo(task.created_at) : "—"}
      </MetaRow>
      {task.completed_at && (
        <MetaRow label={t(($) => $.run_detail.finished)}>
          {timeAgo(task.completed_at)}
          {duration ? ` · ${duration}` : ""}
        </MetaRow>
      )}
      {usage && (
        <MetaRow label={t(($) => $.run_detail.usage)}>
          <span className="tabular-nums">
            {formatTokens(usage.tokens)} · {formatUsd(usage.cost)}
          </span>
        </MetaRow>
      )}

      {(failure || cancelReason) && (
        <div className="space-y-1">
          <SectionLabel>{t(($) => $.run_detail.reason)}</SectionLabel>
          <p className="text-caption text-foreground">{failure ?? cancelReason}</p>
        </div>
      )}
      {task.cancel_requested_by_user_id && (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.run_detail.cancelled_by, {
            name: getMemberName(task.cancel_requested_by_user_id),
            time: task.cancel_requested_at ? timeAgo(task.cancel_requested_at) : "",
          })}
        </p>
      )}
      {task.error && (
        <div className="space-y-1">
          <SectionLabel>{t(($) => $.run_detail.raw_error)}</SectionLabel>
          <pre className="max-h-40 overflow-y-auto whitespace-pre-wrap break-words rounded bg-muted p-2 font-mono text-micro text-muted-foreground">
            {task.error}
          </pre>
        </div>
      )}

      {(ancestors.length > 0 || descendants.length > 0) && (
        <div className="space-y-1">
          <SectionLabel>{t(($) => $.run_detail.lineage)}</SectionLabel>
          <ol className="space-y-1">
            {ancestors.map((entry, i) => (
              <LineageRow
                key={entry.id}
                entry={entry}
                // The edge ABOVE this node is named by the node closer to the
                // current run (its retry child): rerun_of = manual, retry_of
                // = system.
                edge={lineageEdge(entry, ancestors[i + 1] ?? task)}
              />
            ))}
            <li className="flex items-center gap-1.5 rounded bg-accent/50 px-1.5 py-1 text-caption">
              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-info" aria-hidden="true" />
              <span className="truncate">{trigger}</span>
            </li>
            {descendants.map((entry) => (
              <LineageRow
                key={entry.id}
                entry={entry}
                // A descendant names its own parent edge directly.
                edge={entry.rerun_of_task_id ? "rerun" : "system_retry"}
              />
            ))}
          </ol>
        </div>
      )}
    </>
  );
}

function MetaRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 text-caption">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className="min-w-0 truncate text-foreground">{children}</span>
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="text-micro font-medium uppercase tracking-wide text-muted-foreground">
      {children}
    </h3>
  );
}

/**
 * Which lineage column links `child` back to `parent`: a manual rerun
 * (rerun_of_task_id) or a system retry (retry_of_task_id). Both set is not a
 * shape the writers produce; rerun wins the label if it ever appears.
 */
function lineageEdge(parent: RunLineageEntry, child: Pick<RunLineageEntry, "rerun_of_task_id" | "retry_of_task_id">): "rerun" | "system_retry" {
  return child.rerun_of_task_id === parent.id ? "rerun" : "system_retry";
}

function LineageRow({
  entry,
  edge,
}: {
  entry: RunLineageEntry;
  edge: "rerun" | "system_retry";
}) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const statusLabel = useStatusLabel(entry.status as AgentTask["status"]);
  const when = entry.completed_at ?? entry.created_at;

  return (
    <li className="flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
      <TaskStatusIcon status={entry.status as AgentTask["status"]} />
      {entry.attempt > 1 && (
        <span className="shrink-0 tabular-nums">#{entry.attempt}</span>
      )}
      <span className="shrink-0">{statusLabel}</span>
      <span className="shrink-0 rounded border border-border px-1 text-micro">
        {edge === "rerun"
          ? t(($) => $.run_detail.lineage_rerun)
          : t(($) => $.run_detail.lineage_system_retry)}
      </span>
      {when && <span className="ml-auto shrink-0 tabular-nums">{timeAgo(when)}</span>}
    </li>
  );
}
