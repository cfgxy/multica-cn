"use client";

import { useState } from "react";
import type { IssueDecision } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { DecisionCardBrowser } from "./decision-card-browser";

// RUYI-863: the info-feed row is the single-card entry of the unified
// browser. Clicking opens the browser pre-filtered to "all" and PINS this
// card — an already-answered or cancelled card lands exactly here and
// never bounces to the first card. The browser (filter, x/N position,
// previous/next, done/empty states) is shared with every other entry.

export function DecisionTimelineRow({
  decision,
  decisions,
}: {
  decision: IssueDecision;
  /** The issue's full card list, for the browser's navigation. */
  decisions: readonly IssueDecision[];
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const status = decision.status === "open"
    ? t(($) => $.decisions.status_open)
    : decision.status === "answered"
      ? t(($) => $.decisions.status_answered)
      : t(($) => $.decisions.status_cancelled);

  return (
    <>
      <Button
        type="button"
        variant="outline"
        className="mx-2 flex h-auto w-[calc(100%-1rem)] items-center justify-between gap-3 py-2 text-left"
        onClick={() => setOpen(true)}
        data-testid={`decision-summary-${decision.id}`}
      >
        <span className="min-w-0 truncate text-body font-medium">{decision.question}</span>
        <Badge variant={decision.status === "open" ? "secondary" : "outline"} className="shrink-0">{status}</Badge>
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="pr-8">{t(($) => $.decisions.section_all)}</DialogTitle>
          </DialogHeader>
          <DecisionCardBrowser
            decisions={decisions}
            initialFilter="all"
            initialCardId={decision.id}
          />
        </DialogContent>
      </Dialog>
    </>
  );
}
