"use client";

import {
  type AgentTask,
  type RetrospectiveRun,
  type RetrospectiveRunDetail,
} from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { formatDuration } from "../../agents/components/agent-activity-hover-content";
import { TranscriptButton } from "../../common/task-transcript";
import { IssueChip } from "../../issues/components/issue-chip";
import { runErrorLine } from "./retrospective-run-error";

type SelfEvolutionT = ReturnType<typeof useT<"self-evolution">>["t"];

/**
 * Shared status-badge vocabulary for the run list and the detail sheet: the
 * server's three run statuses localize here; anything else renders raw so a
 * newer server's status is still visible (RUYI-663 detail surface).
 */
export function retrospectiveStatusLabel(status: string, t: SelfEvolutionT): string {
  switch (status) {
    case "succeeded": return t(($) => $.retrospective.status.succeeded);
    case "failed": return t(($) => $.retrospective.status.failed);
    case "running": return t(($) => $.retrospective.status.running);
    default: return status;
  }
}

function retrospectiveTriggerLabel(trigger: string, t: SelfEvolutionT): string {
  switch (trigger) {
    case "schedule": return t(($) => $.retrospective.triggerSource.schedule);
    case "manual": return t(($) => $.retrospective.triggerSource.manual);
    default: return trigger;
  }
}

// The run detail is untyped on the wire (raw JSONB passthrough), so every
// field is validated before use — same narrow-parse discipline as
// readRunReason in retrospective-run-error.ts.
function readIssueIds(detail: RetrospectiveRunDetail | null | undefined, field: string): string[] {
  const raw = (detail as { [k: string]: unknown } | null | undefined)?.[field];
  if (!Array.isArray(raw)) return [];
  return raw.filter((id): id is string => typeof id === "string" && id.length > 0);
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5" data-testid="retrospective-detail-row">
      <span className="text-caption text-muted-foreground">{label}</span>
      <span className="text-body">{children}</span>
    </div>
  );
}

/**
 * The five counters are terminal-state columns: the runner only writes them
 * in finishRun, while the detail membership lists go live at trigger time
 * (RUYI-742). Mid-run, deriving scanned/analyzed from those lists and holding
 * the three still-unknown counters at an em dash keeps this line consistent
 * with the lists below it, instead of reading 「扫描 0」 beside 20 scanned
 * issues. Terminal runs read the authoritative columns.
 */
export function runStatsLine(run: RetrospectiveRun, t: SelfEvolutionT): string {
  const cells =
    run.status === "running"
      ? [
          String(readIssueIds(run.detail, "issue_ids").length),
          String(readIssueIds(run.detail, "analyzed_issue_ids").length),
          "—",
          "—",
          "—",
        ]
      : [
          String(run.issues_scanned),
          String(run.issues_analyzed),
          String(run.proposals_created),
          String(run.proposals_merged),
          String(run.duplicates_skipped),
        ];
  const labels = [
    t(($) => $.retrospective.scanned),
    t(($) => $.retrospective.analyzed),
    t(($) => $.retrospective.created),
    t(($) => $.retrospective.merged),
    t(($) => $.retrospective.duplicates),
  ];
  return labels.map((label, i) => `${label} ${cells[i]}`).join(" · ");
}

function IssueLinkList({ ids }: { ids: string[] }) {
  const { t } = useT("self-evolution");
  const paths = useWorkspacePaths();
  if (ids.length === 0) {
    return <span className="text-muted-foreground">{t(($) => $.retrospective.noIssues)}</span>;
  }
  // Platform-common issue rows (RUYI-742): each id renders as the shared
  // IssueChip (status icon + identifier + truncated title) — the same shell
  // the proposal tab wraps in an AppLink — never the bare UUID. An id that
  // no longer resolves keeps its slot as the raw id inside the chip.
  return (
    <ul className="flex flex-col gap-1">
      {ids.map((id) => (
        <li key={id}>
          <AppLink
            href={paths.issueDetail(id)}
            className="rounded-md hover:bg-accent"
            data-testid={`retrospective-issue-link-${id}`}
          >
            <IssueChip issueId={id} fallbackLabel={id} />
          </AppLink>
        </li>
      ))}
    </ul>
  );
}

/**
 * One retrospective run's full record (RUYI-663): trigger, window, timing,
 * the five counters, the localized failure verdict (same runErrorLine
 * semantics as the row — internal copy never surfaces), the scanned and
 * analyzed issue membership with per-issue links, and — when the run
 * actually enqueued a platform task — the live transcript entry.
 */
export function RetrospectiveRunDetailSheet({
  run,
  agentId,
  agentName,
  onOpenChange,
}: {
  /** The clicked run; `null` keeps the sheet closed. */
  run: RetrospectiveRun | null;
  agentId: string;
  agentName: string;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("self-evolution");

  // The transcript entry exists only for runs that got as far as enqueueing
  // a platform task; everything else has nothing to stream.
  const syntheticTask: AgentTask | null = run?.task_id
    ? {
        id: run.task_id,
        agent_id: agentId,
        runtime_id: "",
        issue_id: "",
        status:
          run.status === "running" ? "running" :
          run.status === "succeeded" ? "completed" :
          run.status === "failed" ? "failed" :
          "queued",
        priority: 0,
        dispatched_at: null,
        started_at: run.created_at,
        completed_at: run.finished_at ?? null,
        result: null,
        error: run.error || null,
        created_at: run.created_at,
      }
    : null;

  const finishedMs = run?.finished_at ? new Date(run.finished_at).getTime() : NaN;
  const duration = run && Number.isFinite(finishedMs)
    ? formatDuration(run.created_at, finishedMs)
    : null;
  const errorLine = run ? runErrorLine(run, t) : null;

  return (
    <Sheet open={!!run} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex w-[360px] flex-col gap-4 overflow-y-auto p-4 sm:max-w-[400px]"
      >
        {run && (
          <>
            <SheetHeader>
              <SheetTitle>{t(($) => $.retrospective.detailTitle)}</SheetTitle>
              <SheetDescription className="font-mono text-caption">{run.id}</SheetDescription>
            </SheetHeader>

            <div className="flex items-center gap-2">
              <Badge
                variant={run.status === "succeeded" ? "default" : run.status === "failed" ? "destructive" : "secondary"}
                data-testid="retrospective-detail-status"
              >
                {retrospectiveStatusLabel(run.status, t)}
              </Badge>
              {syntheticTask && (
                <TranscriptButton
                  task={syntheticTask}
                  agentName={agentName}
                  isLive={run.status === "running"}
                  title={t(($) => $.retrospective.viewLog)}
                />
              )}
            </div>

            <div className="flex flex-col gap-3">
              <DetailRow label={t(($) => $.retrospective.triggerLabel)}>
                {retrospectiveTriggerLabel(run.trigger, t)}
              </DetailRow>
              <DetailRow label={t(($) => $.retrospective.windowLabel)}>
                {new Date(run.window_start).toLocaleString()} –{" "}
                {new Date(run.window_end).toLocaleString()}
              </DetailRow>
              <DetailRow label={t(($) => $.retrospective.startedAt)}>
                {new Date(run.created_at).toLocaleString()}
              </DetailRow>
              <DetailRow label={t(($) => $.retrospective.finishedAt)}>
                {run.finished_at ? new Date(run.finished_at).toLocaleString() : t(($) => $.retrospective.durationRunning)}
              </DetailRow>
              <DetailRow label={t(($) => $.retrospective.duration)}>
                {duration ?? t(($) => $.retrospective.durationRunning)}
              </DetailRow>
            </div>

            <div className="flex flex-col gap-1 border-t pt-3">
              <span className="text-caption text-muted-foreground">{t(($) => $.retrospective.statsLabel)}</span>
              <div className="text-body" data-testid="retrospective-detail-stats">
                {runStatsLine(run, t)}
              </div>
            </div>

            {errorLine ? (
              <div className="flex flex-col gap-1 border-t pt-3" data-testid="retrospective-detail-error">
                <span className="text-caption text-muted-foreground">{t(($) => $.retrospective.failureLabel)}</span>
                <span className="text-destructive text-body">{errorLine}</span>
              </div>
            ) : null}

            <div className="flex flex-col gap-1 border-t pt-3">
              <span className="text-caption text-muted-foreground">{t(($) => $.retrospective.scannedIssues)}</span>
              <IssueLinkList ids={readIssueIds(run.detail, "issue_ids")} />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-caption text-muted-foreground">{t(($) => $.retrospective.analyzedIssues)}</span>
              <IssueLinkList ids={readIssueIds(run.detail, "analyzed_issue_ids")} />
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
