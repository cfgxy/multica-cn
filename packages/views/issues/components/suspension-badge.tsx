"use client";

import { memo } from "react";
import { CirclePause } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import type { Issue } from "@multica/core/types";
import { useT } from "../../i18n";

/**
 * Purple "挂起 / Suspended" pill for issues with an active suspension
 * (RUYI-690). Rendered in the board card's and list row's run-status signal
 * slot, beside — not exclusive with — SuppressedBadge: run suppression holds
 * the next run, suspension holds the whole issue, and both facts can be
 * true at once.
 *
 * Negative contract, mirroring SuppressedBadge: anything other than a set
 * `suspended_at` (null / undefined from an older backend) renders nothing —
 * the card stays pixel-identical to the pre-RUYI-690 layout. Unlike the run
 * hold there is no "moving outranks parked" yield: while suspended, comment
 * and @ triggers queue no runs server-side, so the issue cannot be moving
 * through this dimension.
 */
export const SuspensionBadge = memo(function SuspensionBadge({
  issue,
}: {
  issue: Issue;
}) {
  const { t } = useT("issues");

  if (!issue.suspended_at) return null;

  return (
    <Tooltip>
      {/* A span trigger never intercepts the card's AppLink click; the icon
          is aria-hidden and both semantics live in the visible text + the
          tooltip, matching SuppressedBadge's a11y notes. */}
      <TooltipTrigger
        render={
          <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-violet-500/10 px-1.5 py-0.5 text-micro text-violet-600 dark:text-violet-400">
            <CirclePause className="size-3" aria-hidden />
            <span>{t(($) => $.suspension.badge)}</span>
          </span>
        }
      />
      <TooltipContent>{t(($) => $.suspension.tooltip)}</TooltipContent>
    </Tooltip>
  );
});
