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
        <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t(($) => $.decisions.batch_title)}</DialogTitle>
          </DialogHeader>
          <DecisionBatchBar issueId={issueId} open={open} />
        </DialogContent>
      </Dialog>
    </>
  );
}
