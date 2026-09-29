"use client";

import { memo, useCallback } from "react";
import { CirclePause } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Issue } from "@multica/core/types";
import { selectIssueTasks, type IssueTaskGroups } from "../surface/activity";
import { useT } from "../../i18n";

const EMPTY_GROUPS: IssueTaskGroups = { running: [], queued: [] };

/**
 * Amber "已暂缓 / On hold" pill for issues whose latest run-producing write
 * was an honored `suppress_run` (RUYI-275). Rendered in the board card's
 * Row 1 right slot and in list rows right after the identifier — the same
 * slot IssueAgentActivityIndicator occupies, hence the mutual exclusion
 * with the activity indicator winning (below).
 *
 * Negative contract (the AC the badge exists to satisfy): anything other
 * than `run_suppressed === true` — false, or undefined from an older
 * backend that predates the field — renders nothing. No placeholder, no
 * skeleton, so the card is pixel-identical to the pre-RUYI-275 layout.
 *
 * Indicator-first mutual exclusion: a suppressed issue that also has a live
 * or queued task (an agent was started some other way, e.g. a mention run)
 * is "moving", which outranks "parked". Rather than coordinating two
 * components through a parent, this component subscribes to the same
 * workspace task snapshot the indicator uses — one shared query entry, so
 * the subscription is a cache hit, and React Query's structural sharing
 * keeps the selected groups referentially stable — and yields whenever the
 * indicator would render.
 */
export const SuppressedBadge = memo(function SuppressedBadge({
  issue,
}: {
  issue: Issue;
}) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const issueId = issue.id;
  const select = useCallback(
    (snapshot: Parameters<typeof selectIssueTasks>[0]) =>
      selectIssueTasks(snapshot, issueId),
    [issueId],
  );
  const { data: groups = EMPTY_GROUPS } = useQuery({
    ...agentTaskSnapshotOptions(wsId),
    select,
  });

  if (issue.run_suppressed !== true) return null;
  if (groups.running.length > 0 || groups.queued.length > 0) return null;

  return (
    <Tooltip>
      {/* The trigger is a span, so it never intercepts the card's AppLink
          click; the icon is aria-hidden and both semantics live in the
          visible text + the tooltip, per the UX spec's a11y notes. */}
      <TooltipTrigger
        render={
          <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-500/10 px-1.5 py-0.5 text-micro text-amber-600 dark:text-amber-400">
            <CirclePause className="size-3" aria-hidden />
            <span>{t(($) => $.suppressed.badge)}</span>
          </span>
        }
      />
      <TooltipContent>{t(($) => $.suppressed.tooltip)}</TooltipContent>
    </Tooltip>
  );
});
