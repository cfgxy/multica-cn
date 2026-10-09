"use client";

import type { ReactNode } from "react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { ConfigSourceBadge, type ConfigSource } from "./config-source-badge";

/**
 * The four-state data-source card (RUYI-551 §2.5 / walk #8): every state must
 * answer 状态 → 原因 → 影响 → 操作 in one card. The status dot color is the
 * health vocabulary of §5.3 (green ok / red error / amber disabled / gray
 * unconfigured); the effective source rides the neutral ConfigSourceBadge.
 */
export type DataSourceStatus =
  | "ok"
  | "unconfigured"
  | "error"
  | "disabled";

export function DataSourceStatusCard({
  title,
  status,
  effectiveSource,
  reason,
  impact,
  actions,
  testId,
}: {
  title: string;
  status: DataSourceStatus;
  /** Neutral origin badge; omit when nothing is configured. */
  effectiveSource?: ConfigSource;
  /** Why the source is in this state (backend-derived, already localized). */
  reason: ReactNode;
  /** What the state means for the page's capabilities. */
  impact: ReactNode;
  /** The actionable fix — always at least one (walk #8's closure rule). */
  actions?: ReactNode;
  testId?: string;
}) {
  const { t } = useT("self-evolution");
  return (
    <Card className="gap-0 py-0 shadow-none" data-testid={testId}>
      <CardContent className="flex flex-col gap-3 px-5 py-4">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <span className="text-body font-semibold">{title}</span>
            <span
              aria-hidden
              className={cn(
                "inline-block size-2 rounded-full",
                status === "ok" && "bg-emerald-500",
                status === "error" && "bg-destructive",
                status === "disabled" && "bg-amber-500",
                status === "unconfigured" && "bg-muted-foreground/40",
              )}
              data-status={status}
            />
            <span className="text-caption text-muted-foreground">
              {t(($) => $.sources.states[status])}
            </span>
          </div>
          {effectiveSource ? <ConfigSourceBadge source={effectiveSource} /> : null}
        </div>
        <div className="flex flex-col gap-1.5 text-caption leading-5">
          <div className="flex gap-1.5">
            <span className="shrink-0 text-muted-foreground">
              {t(($) => $.sources.reason)}
            </span>
            <span className="min-w-0">{reason}</span>
          </div>
          <div className="flex gap-1.5">
            <span className="shrink-0 text-muted-foreground">
              {t(($) => $.sources.impact)}
            </span>
            <span className="min-w-0">{impact}</span>
          </div>
        </div>
        {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
      </CardContent>
    </Card>
  );
}
