"use client";

import { useMemo, useState } from "react";
import { ListChecks } from "lucide-react";
import type { IssueDecision } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { DecisionCardBrowser } from "./decision-card-browser";

// RUYI-863: the batch entry is one of the three entries of the SAME
// per-card browser — it no longer owns RUYI-471's submit-all bar. Its
// entry parameter is the "open" filter: the browser lands on the first
// pending card, answering each card (single-card mutation, server CAS)
// walks forward through the pending list, and the count/position update
// in place; when the pending set empties the browser shows its done
// state instead of bouncing the user back to the feed. The batch entry
// shows itself while two or more cards are open, matching the old bar.

export function DecisionBatchEntry({
  decisions,
}: {
  /** The issue's full card list — the browser's data source. */
  decisions: readonly IssueDecision[];
}) {
  const { t } = useT("issues");
  const [showBatch, setShowBatch] = useState(false);

  // Authorization cards are never "pending" in the batch sense (their
  // lifecycle lives on the decision request), and created_at order is
  // the batch numbering the server echoes.
  const open = useMemo(
    () =>
      decisions
        .filter(
          (d) => d.status === "open" && d.decision_kind !== "authorization",
        )
        .slice()
        .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at)),
    [decisions],
  );

  if (open.length < 2) return null;

  return (
    <>
      <Button
        type="button"
        variant="outline"
        className="mx-2 flex h-auto w-[calc(100%-1rem)] items-center justify-start gap-3 py-3 text-left"
        onClick={() => setShowBatch(true)}
        data-testid="decision-batch-entry"
      >
        <ListChecks className="size-4 shrink-0 text-brand" aria-hidden />
        <span className="min-w-0 flex-1 truncate text-body font-medium">{t(($) => $.decisions.batch_title)}</span>
        <span className="shrink-0 text-caption text-muted-foreground">{t(($) => $.decisions.batch_count, { count: open.length })}</span>
      </Button>
      <Dialog open={showBatch} onOpenChange={setShowBatch}>
        {/* RUYI-861: width contract — see decision-timeline-row. The base
            `sm:max-w-sm` shadowed the plain `max-w-2xl`; the important
            breakpoint-scoped width reads at the issue body column
            (`max-w-4xl` = 56rem) and clamps to the viewport gutters. */}
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:!max-w-[min(56rem,calc(100%_-_2rem))]">
          <DialogHeader>
            <DialogTitle>{t(($) => $.decisions.batch_title)}</DialogTitle>
          </DialogHeader>
          <DecisionCardBrowser decisions={decisions} initialFilter="open" />
        </DialogContent>
      </Dialog>
    </>
  );
}
