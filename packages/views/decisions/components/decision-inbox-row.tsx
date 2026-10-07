"use client";

import { AppLink } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";
import type { WorkspaceDecisionInboxItem } from "@multica/core/types";
import { DecisionRecommendedBadge } from "./decision-recommended-badge";

/**
 * One row of the Decision Center list (RUYI-494). The data unit is the CARD,
 * not the issue, and the row is fixed two lines:
 *   line 1 — the issue identifier (e.g. "RUYI-494", always visible) plus the
 *            issue title it belongs to;
 *   line 2 — this card's own question.
 * Multiple cards on one issue therefore render as multiple rows, and the row
 * deep-links to the card itself via `#decision-<id>` on the issue page.
 * Hover treatment follows the issues list rows (RUYI-547) so the two surfaces
 * read as one product.
 */
export function DecisionInboxRow({ item, href }: { item: WorkspaceDecisionInboxItem; href: string }) {
  const { t } = useT("decisions");
  const timeAgo = useTimeAgo();

  return (
    <AppLink
      href={href}
      data-testid="decision-inbox-row"
      data-status={item.status}
      className="flex flex-col gap-1 rounded-lg px-3 py-2 hover:bg-surface-hover"
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
          <DecisionRecommendedBadge label={t(($) => $.badge.recommended)} />
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
