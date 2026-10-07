"use client";

import { AppLink } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";
import type { WorkspaceDecisionInboxItem } from "@multica/core/types";

/**
 * One row of the Decision Center list (RUYI-494). The data unit is the CARD,
 * not the issue, and the row is fixed two lines:
 *   line 1 — the issue identifier (e.g. "RUYI-494", always visible) plus the
 *            issue title it belongs to;
 *   line 2 — this card's own question.
 * Multiple cards on one issue therefore render as multiple rows, and the row
 * deep-links to the card itself via `#decision-<id>` on the issue page.
 */
export function DecisionInboxRow({ item, href }: { item: WorkspaceDecisionInboxItem; href: string }) {
  const { t } = useT("decisions");
  const timeAgo = useTimeAgo();

  return (
    <AppLink
      href={href}
      data-testid="decision-inbox-row"
      data-status={item.status}
      className="flex flex-col gap-1 rounded-lg border border-transparent px-3 py-2.5 hover:border-border hover:bg-sidebar-accent/50"
    >
      <span className="flex min-w-0 items-center gap-2">
        <span
          data-testid="decision-inbox-identifier"
          className="shrink-0 font-mono text-caption text-muted-foreground"
        >
          {item.issue_identifier || `#${item.issue_number}`}
        </span>
        <span className="truncate text-body font-medium">{item.issue_title}</span>
        {item.recommended_indices.length > 0 && (
          <span
            data-testid="decision-inbox-recommended"
            className="shrink-0 rounded-full bg-brand/10 px-1.5 py-0.5 text-micro font-medium text-brand"
          >
            {t(($) => $.badge.recommended)}
          </span>
        )}
      </span>
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate text-caption text-muted-foreground">{item.question}</span>
        <span className="ml-auto shrink-0 text-micro text-faint-foreground">
          {timeAgo(item.created_at)}
        </span>
      </span>
    </AppLink>
  );
}
