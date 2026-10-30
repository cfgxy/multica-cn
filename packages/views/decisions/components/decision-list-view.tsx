"use client";

import { memo, useMemo } from "react";
import type { IssueDecisionStatus, WorkspaceDecisionInboxItem } from "@multica/core/types";
import { useT } from "../../i18n";
import { DecisionInboxRow } from "./decision-inbox-row";
import {
  DECISION_STATUS_CONFIG,
  DECISION_STATUS_ORDER,
} from "./decision-status-config";

// Decision list (RUYI-547): status sections whose sticky headers take the
// issues list's section shape (h-10 muted bar, status icon + label + count).
// The data unit is still the CARD — one row per card, never folded by issue.
// RUYI-864: the server owns the filter window (one status or "all" plus the
// keyword), so this view just groups whatever window came back by status; a
// single-status window renders as one section.

interface DecisionListViewProps {
  items: WorkspaceDecisionInboxItem[];
  onSelect: (item: WorkspaceDecisionInboxItem) => void;
  /** Counts scoped to the list's filter window (status + keyword, never the
   * page window) — section numbers stay true even after paging. */
  counts: Record<IssueDecisionStatus, number>;
}
export const DecisionListView = memo(function DecisionListView({
  items,
  onSelect,
  counts,
}: DecisionListViewProps) {
  const byStatus = useMemo(() => {
    const map = new Map<IssueDecisionStatus, WorkspaceDecisionInboxItem[]>(
      DECISION_STATUS_ORDER.map((s) => [s, []]),
    );
    for (const item of items) {
      map.get(item.status)?.push(item);
    }
    return map;
  }, [items]);

  return (
    <div className="flex-1 min-h-0 overflow-y-auto px-4 py-2" data-testid="decision-list-view">
      {DECISION_STATUS_ORDER.map((status) => {
        const sectionItems = byStatus.get(status) ?? [];
        if (sectionItems.length === 0) return null;
        return (
          <section key={status} data-testid={`decision-section-${status}`} className="mb-3">
            <DecisionSectionHeading status={status} count={counts[status]} />
            <div className="flex flex-col gap-1">
              {sectionItems.map((item) => (
                <DecisionInboxRow key={item.id} item={item} onSelect={onSelect} />
              ))}
            </div>
          </section>
        );
      })}
    </div>
  );
});

function DecisionSectionHeading({
  status,
  count,
}: {
  status: IssueDecisionStatus;
  count: number;
}) {
  const { t } = useT("decisions");
  const cfg = DECISION_STATUS_CONFIG[status];
  const Icon = cfg.icon;

  return (
    <div className="sticky top-0 z-10 mb-1.5 flex h-10 items-center gap-2 rounded-lg bg-muted px-3 transition-colors">
      <span className="inline-flex items-center gap-1.5 text-caption font-semibold">
        <Icon className={`h-3 w-3 ${cfg.iconColor}`} aria-hidden />
        {t(($) => $.section[status])}
      </span>
      <span className="text-caption text-muted-foreground">{count}</span>
    </div>
  );
}
