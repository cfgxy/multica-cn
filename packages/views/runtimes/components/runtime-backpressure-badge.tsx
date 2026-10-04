"use client";

import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import type { AgentRuntime } from "@multica/core/types";
import { readRuntimeBackpressure } from "@multica/core/runtimes";
import { useT } from "../../i18n";

// Host-memory backpressure badge (RUYI-393). The daemon pauses new task
// claims while its machine runs low on memory/swap and reports the state on
// every heartbeat; the server stores it in the runtime row's metadata. Only
// renders while ACTIVE — an inactive report means "healthy", which is the
// default and would be chip noise on every row (same rule as the visibility
// badge). The tooltip carries the queued reason and the deferred-claim count,
// so a user staring at a stuck run can tell why nothing is being picked up.
export function BackpressureBadge({ runtime }: { runtime: AgentRuntime }) {
  const { t } = useT("runtimes");
  const bp = readRuntimeBackpressure(runtime.metadata);
  if (!bp?.active) return null;
  const reasonText =
    bp.reason === "mem"
      ? t(($) => $.detail.bp_reason_mem)
      : bp.reason === "swap"
        ? t(($) => $.detail.bp_reason_swap)
        : t(($) => $.detail.bp_reason_mem_swap);
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            data-testid="backpressure-badge"
            className="inline-flex shrink-0 items-center rounded bg-warning/10 px-1 text-micro font-medium text-warning"
          >
            {t(($) => $.list.badge_backpressure)}
          </span>
        }
      />
      <TooltipContent>
        {t(($) => $.detail.backpressure_hint, {
          reason: reasonText,
          mem: bp.memAvailablePct,
          swap: bp.swapUsedPct,
          count: bp.deferredClaims,
        })}
      </TooltipContent>
    </Tooltip>
  );
}
