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
  // The reason is a "+"-joined token list ("mem", "swap", "psi"); compose the
  // localized text per token so every combination reads naturally without one
  // locale entry per permutation.
  const tokenText: Record<string, string> = {
    mem: t(($) => $.detail.bp_reason_mem),
    swap: t(($) => $.detail.bp_reason_swap),
    psi: t(($) => $.detail.bp_reason_psi),
  };
  const condLabel = (condition: string): string => {
    const map: Record<string, string> = {
      mem: t(($) => $.detail.bp_cond_mem),
      swap: t(($) => $.detail.bp_cond_swap),
      psi: t(($) => $.detail.bp_cond_psi),
    };
    return map[condition] ?? condition;
  };
  const reasonText = bp.reason
    .split("+")
    .map((tok) => tokenText[tok.trim()] ?? tok.trim())
    .join(" + ");
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
        <span className="block">
          {t(($) => $.detail.backpressure_hint, {
            reason: reasonText,
            mem: bp.memAvailablePct,
            swap: bp.swapUsedPct,
            count: bp.deferredClaims,
          })}
        </span>
        {bp.psiReadOK && (
          <span className="block">
            {t(($) => $.detail.backpressure_psi_hint, { psi: bp.psiSomeAvg10 })}
          </span>
        )}
        {/* Stage-1 daemons echo the card they judged (RUYI-729): pair every
            reading with its threshold so "is 62% bad?" needs no guessing.
            Pre-stage-1 reports have neither field — degrade to the bare
            numbers above, never inventing a threshold. */}
        {bp.conditions && bp.conditions.length > 0 && (
          <span className="mt-1 block border-t border-white/15 pt-1">
            {bp.conditions.map((c) => (
              <span key={c.condition} className="block">
                {condLabel(c.condition)}:{" "}
                {t(($) => $.detail.bp_vs_threshold, {
                  value: c.value,
                  threshold: c.threshold,
                })}
              </span>
            ))}
          </span>
        )}
      </TooltipContent>
    </Tooltip>
  );
}
