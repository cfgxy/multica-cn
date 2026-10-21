"use client";

import { CirclePlay, X } from "lucide-react";
import { useReleaseSuspension } from "@multica/core/issues/suspensions";
import { useWorkspacePaths } from "@multica/core/paths";
import type { WorkspaceIssueSuspension } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT, useTimeAgo } from "../../i18n";
import { AppLink } from "../../navigation";

/**
 * One row of the Suspension Center (RUYI-690). The data unit is the
 * SUSPENSION, not the issue:
 *   line 1 — issue identifier + title + lifecycle status (suspension never
 *            rewrites it, so showing both answers "what is it doing now");
 *   line 2 — the wait target summary and, when set, the free-text resume
 *            condition; release actions live on the right for active rows.
 * Hover treatment follows the issues list rows / decision inbox rows so the
 * three aggregation surfaces read as one product.
 */
export function SuspensionRow({ suspension }: { suspension: WorkspaceIssueSuspension }) {
  const { t } = useT("suspensions");
  const timeAgo = useTimeAgo();
  const p = useWorkspacePaths();

  return (
    <div
      className="flex flex-col gap-1 rounded-lg px-3 py-2 hover:bg-surface-hover"
      data-testid="suspension-row"
      data-status={suspension.status}
    >
      <span className="flex min-w-0 items-center gap-2">
        <AppLink
          href={p.issueDetail(suspension.issue_id)}
          className="flex min-w-0 flex-1 items-center gap-2"
        >
          <span
            data-testid="suspension-row-identifier"
            className="shrink-0 font-mono text-caption text-muted-foreground"
          >
            {suspension.issue_identifier}
          </span>
          <span className="truncate text-body font-medium">
            {suspension.issue_title}
          </span>
        </AppLink>
        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-micro text-muted-foreground">
          {suspension.issue_status}
        </span>
        <SuspensionReleaseButtons suspension={suspension} />
      </span>
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate text-caption text-muted-foreground">
          {suspension.waiting_on_summary}
          {suspension.resume_condition
            ? ` · ${t(($) => $.row.condition)}: ${suspension.resume_condition}`
            : ""}
        </span>
        <span className="ml-auto shrink-0 text-micro text-faint-foreground">
          {timeAgo(suspension.suspended_at)}
        </span>
      </span>
    </div>
  );
}

/**
 * Resume/cancel button pair for one aggregate row. Only active rows render
 * it; both release paths are CAS server-side — a stale row that lost the
 * race surfaces the conflict as a rejected mutation and the aggregation
 * refetches through the shared invalidation.
 */
export function SuspensionReleaseButtons({ suspension }: { suspension: WorkspaceIssueSuspension }) {
  const { t } = useT("suspensions");
  const resume = useReleaseSuspension("resume");
  const cancel = useReleaseSuspension("cancel");
  const busy = resume.isPending || cancel.isPending;

  if (suspension.status !== "active") return null;

  return (
    <span
      className="flex shrink-0 items-center gap-1"
      onClick={(e) => e.stopPropagation()}
    >
      <Button
        variant="outline"
        size="sm"
        className="h-6 gap-1 px-2 text-caption"
        disabled={busy}
        data-testid={`suspension-resume-${suspension.id}`}
        onClick={() =>
          resume.mutate({
            issueId: suspension.issue_id,
            suspensionId: suspension.id,
          })
        }
      >
        <CirclePlay className="size-3" />
        {t(($) => $.row.resume)}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="h-6 gap-1 px-2 text-caption text-muted-foreground"
        disabled={busy}
        data-testid={`suspension-cancel-${suspension.id}`}
        onClick={() =>
          cancel.mutate({
            issueId: suspension.issue_id,
            suspensionId: suspension.id,
          })
        }
      >
        <X className="size-3" />
        {t(($) => $.row.cancel)}
      </Button>
    </span>
  );
}
