"use client";

import type { ReactNode } from "react";
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { useT } from "../../i18n";

/**
 * The segmented list toolbar (RUYI-551 §5.2): the segment tabs (with counts)
 * on the left, the page-level CTA on the far right, and the filter row
 * beneath — the one shape every browse-heavy page in the module shares
 * (walk #18: segments, not stacked cards).
 */
export interface EntitySegment {
  value: string;
  label: string;
  count?: number;
}

export function EntitySegToolbar({
  segments,
  value,
  onValueChange,
  filters,
  action,
}: {
  segments: EntitySegment[];
  value: string;
  onValueChange: (next: string) => void;
  /** Selects / search — rendered right-aligned above the table. */
  filters?: ReactNode;
  /** The page-level primary action (e.g. ＋ 添加外部来源). */
  action?: ReactNode;
}) {
  const { t } = useT("self-evolution");
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs value={value} onValueChange={(next) => { if (typeof next === "string") onValueChange(next); }}>
          <TabsList>
            {segments.map((seg) => (
              <TabsTrigger key={seg.value} value={seg.value}>
                {seg.label}
                {typeof seg.count === "number" ? (
                  <span className="ml-1.5 text-caption text-muted-foreground">
                    {seg.count}
                  </span>
                ) : null}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        {action ? <div className="flex shrink-0 items-center gap-2">{action}</div> : null}
      </div>
      {filters ? (
        <div className="flex flex-wrap items-center justify-end gap-2" aria-label={t(($) => $.segToolbar.filters)}>
          {filters}
        </div>
      ) : null}
    </div>
  );
}
