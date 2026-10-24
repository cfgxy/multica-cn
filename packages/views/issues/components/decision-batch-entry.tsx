"use client";

import { useEffect, useState } from "react";
import { ListChecks } from "lucide-react";
import type { IssueDecision } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { DecisionBatchBar } from "./decision-batch-bar";

export function DecisionBatchEntry({ issueId, open }: { issueId: string; open: IssueDecision[] }) {
  const { t } = useT("issues");
  const [showBatch, setShowBatch] = useState(false);
  useEffect(() => {
    if (open.length < 2) setShowBatch(false);
  }, [open.length]);

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
          <DecisionBatchBar issueId={issueId} open={open} />
        </DialogContent>
      </Dialog>
    </>
  );
}
