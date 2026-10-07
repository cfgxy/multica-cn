"use client";

import { memo, useMemo } from "react";
import type { IssueDecisionStatus, WorkspaceDecisionInboxItem } from "@multica/core/types";
import { DECISION_ANCHOR_PREFIX, useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";
import {
  DECISION_STATUS_CONFIG,
  DECISION_STATUS_ORDER,
} from "./decision-status-config";
import { DecisionRecommendedBadge } from "./decision-recommended-badge";

// Decision board (RUYI-547): one column per decision status, laid out with
// the issues board's geometry — 280px rounded columns on a tinted background,
// cards shaped like BoardCardContent (identifier row, clamped title, clamped
// secondary line). Cards are links to their decision card on the issue, not
// draggable: a decision's status only changes through its own answer/cancel
// operations, so there is no move to perform between columns.

export const DECISION_COL_WIDTH = 280;

interface DecisionBoardViewProps {
  items: WorkspaceDecisionInboxItem[];
  /** Workspace-wide totals, same source as the list section headers. */
  counts: Record<IssueDecisionStatus, number>;
  visibleStatuses: IssueDecisionStatus[];
}

export const DecisionBoardView = memo(function DecisionBoardView({
  items,
  counts,
  visibleStatuses,
}: DecisionBoardViewProps) {
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
    <div
      className="flex flex-1 min-h-0 gap-4 overflow-x-auto p-2"
      data-testid="decision-board-view"
    >
      {DECISION_STATUS_ORDER.filter((status) => visibleStatuses.includes(status)).map(
        (status) => (
          <DecisionBoardColumn
            key={status}
            status={status}
            items={byStatus.get(status) ?? []}
            count={counts[status]}
          />
        ),
      )}
    </div>
  );
});

const DecisionBoardColumn = memo(function DecisionBoardColumn({
  status,
  items,
  count,
}: {
  status: IssueDecisionStatus;
  items: WorkspaceDecisionInboxItem[];
  count: number;
}) {
  const { t } = useT("decisions");
  const cfg = DECISION_STATUS_CONFIG[status];
  const Icon = cfg.icon;

  return (
    <div
      style={{ width: DECISION_COL_WIDTH }}
      className={`flex shrink-0 flex-col rounded-xl ${cfg.columnBg} p-2`}
      data-testid={`decision-board-column-${status}`}
    >
      <div className="mb-2 flex items-center justify-between px-1.5">
        <div className="flex items-center gap-2">
          <span className="inline-flex items-center gap-1.5 text-caption font-semibold">
            <Icon className={`h-3 w-3 ${cfg.iconColor}`} aria-hidden />
            {t(($) => $.section[status])}
          </span>
          <span className="shrink-0 rounded-full bg-background px-1.5 py-0.5 text-micro font-medium tabular-nums text-muted-foreground">
            {count}
          </span>
        </div>
      </div>
      <div className="min-h-[120px] flex-1 rounded-lg p-1">
        {items.length === 0 ? (
          <p className="py-8 text-center text-caption text-muted-foreground">
            {t(($) => $.board.empty_column)}
          </p>
        ) : (
          <div className="flex flex-col gap-2">
            {items.map((item) => (
              <DecisionBoardCard key={item.id} item={item} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
});

const DecisionBoardCard = memo(function DecisionBoardCard({
  item,
}: {
  item: WorkspaceDecisionInboxItem;
}) {
  const { t } = useT("decisions");
  const timeAgo = useTimeAgo();
  const p = useWorkspacePaths();

  return (
    <AppLink
      href={`${p.issueDetail(item.issue_id)}#${DECISION_ANCHOR_PREFIX}${item.id}`}
      data-testid="decision-board-card"
      data-status={item.status}
      newTabTitle={item.issue_identifier || `#${item.issue_number}`}
      className="block rounded-lg border-[0.5px] border-surface-border bg-surface py-3 px-2.5 shadow-[var(--surface-shadow)] transition-colors hover:border-foreground/15 hover:bg-surface-hover"
    >
      <div className="flex items-center gap-1.5 min-w-0">
        <p className="text-caption text-muted-foreground truncate">
          {item.issue_identifier || `#${item.issue_number}`}
        </p>
        <span className="ml-auto shrink-0 text-micro text-faint-foreground">
          {timeAgo(item.created_at)}
        </span>
      </div>
      <p className="mt-1 text-body font-medium leading-snug line-clamp-2">
        {item.issue_title}
      </p>
      <p className="mt-1 text-caption text-muted-foreground line-clamp-2">
        {item.question}
      </p>
      {item.recommended_indices.length > 0 && (
        <div className="mt-1.5">
          <DecisionRecommendedBadge label={t(($) => $.badge.recommended)} />
        </div>
      )}
    </AppLink>
  );
});
