"use client";

import { useState } from "react";
import type { IssueDecision } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { DecisionCard } from "./decision-card";

export function DecisionTimelineRow({ decision }: { decision: IssueDecision }) {
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
            <DialogTitle className="pr-8">{decision.question}</DialogTitle>
          </DialogHeader>
          <DecisionCard decision={decision} />
        </DialogContent>
      </Dialog>
    </>
  );
}
