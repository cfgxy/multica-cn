"use client";

import { useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, ListChecks } from "lucide-react";
import {
  decisionBrowserNeighbor,
  filterDecisionCards,
  initialDecisionBrowserCardId,
  resolveDecisionBrowserPosition,
  type DecisionCardFilter,
} from "@multica/core/issues/decision-card-browser";
import type { IssueDecision } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";
import { DecisionCard } from "./decision-card";

// RUYI-863: the one card browser every entry opens — info-feed row (pin the
// target card under "all"), batch entry (filter "open" from the first
// pending card), sidebar more-menu entry (first card under "all"). One data
// source (the issue's card list), one per-card view (DecisionCard); entries
// differ only in their initial filter + card. Position rules live in
// @multica/core/issues/decision-card-browser so web, desktop and mobile
// share the same state machine — answering a card under "open" advances to
// the next pending card instead of bouncing the user back to the feed, and
// when the filtered set empties the browser shows its done state.

const FILTER_OPTIONS: readonly DecisionCardFilter[] = [
  "all",
  "open",
  "answered",
  "cancelled",
];

export interface DecisionCardBrowserProps {
  /** The issue's full card list; sorted by created_at here, no order assumed. */
  decisions: readonly IssueDecision[];
  /** Entry parameter: which filter starts. Default "all". */
  initialFilter?: DecisionCardFilter;
  /** Entry parameter: pin this card on open (single-card entry passes it,
   *  with initialFilter "all" so an answered/cancelled target never bounces). */
  initialCardId?: string | null;
}

export function DecisionCardBrowser({
  decisions,
  initialFilter = "all",
  initialCardId,
}: DecisionCardBrowserProps) {
  const { t } = useT("issues");
  const [filter, setFilter] = useState<DecisionCardFilter>(initialFilter);
  const [cardId, setCardId] = useState(() =>
    initialDecisionBrowserCardId(decisions, initialFilter, initialCardId),
  );

  // One sort pass up front; every position rule assumes created_at ASC.
  const sorted = useMemo(
    () =>
      [...decisions].sort(
        (a, b) => Date.parse(a.created_at) - Date.parse(b.created_at),
      ),
    [decisions],
  );

  // The desired card may have left the active filter's bucket (answered
  // while filtering open, restored while filtering cancelled) — re-resolve
  // it on every data/filter change instead of chasing it with setState.
  const resolvedId = resolveDecisionBrowserPosition(sorted, filter, cardId);
  const filtered = filterDecisionCards(sorted, filter);
  const currentIndex = resolvedId
    ? filtered.findIndex((d) => d.id === resolvedId)
    : -1;
  const current = currentIndex >= 0 ? filtered[currentIndex] : undefined;

  const filterItems = FILTER_OPTIONS.map((f) => ({
    value: f,
    label: t(($) =>
      $.decisions[
        f === "all"
          ? "filter_all"
          : f === "open"
            ? "status_open"
            : f === "answered"
              ? "status_answered"
              : "status_cancelled"
      ],
    ),
  }));

  return (
    <div className="flex flex-col gap-3" data-testid="decision-card-browser">
      <div className="flex flex-wrap items-center gap-2">
        <ListChecks className="h-4 w-4 shrink-0 text-brand" aria-hidden />
        <span className="text-caption font-medium text-muted-foreground">
          {t(($) => $.decisions.section_all)}
        </span>
        <Select
          items={filterItems}
          value={filter}
          onValueChange={(value) =>
            value && setFilter(value as DecisionCardFilter)
          }
        >
          <SelectTrigger
            size="sm"
            className="min-w-32"
            data-testid="decision-browser-filter"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {filterItems.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {current && (
          <Badge
            variant="outline"
            className="ml-auto"
            data-testid="decision-browser-position"
          >
            {currentIndex + 1} / {filtered.length}
          </Badge>
        )}
      </div>

      {current ? (
        <>
          <DecisionCard decision={current} />
          <div className="flex items-center justify-between gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={currentIndex === 0}
              onClick={() => {
                const id = decisionBrowserNeighbor(sorted, filter, resolvedId, "previous");
                if (id) setCardId(id);
              }}
              data-testid="decision-browser-previous"
            >
              <ChevronLeft aria-hidden />
              {t(($) => $.decisions.previous)}
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={currentIndex === filtered.length - 1}
              onClick={() => {
                const id = decisionBrowserNeighbor(sorted, filter, resolvedId, "next");
                if (id) setCardId(id);
              }}
              data-testid="decision-browser-next"
            >
              {t(($) => $.decisions.next)}
              <ChevronRight aria-hidden />
            </Button>
          </div>
        </>
      ) : filter === "open" ? (
        <p
          className="py-4 text-center text-caption text-muted-foreground"
          data-testid="decision-browser-done"
        >
          {t(($) => $.decisions.browser_done)}
        </p>
      ) : (
        <p
          className="py-4 text-center text-caption text-muted-foreground"
          data-testid="decision-browser-empty"
        >
          {t(($) => $.decisions[filter === "all" ? "empty_issue" : "filter_empty"])}
        </p>
      )}
    </div>
  );
}
