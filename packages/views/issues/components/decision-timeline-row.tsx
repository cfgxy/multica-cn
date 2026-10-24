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
        {/* RUYI-861: the base `sm:max-w-sm` (384px) sits in the media block
            that Tailwind emits AFTER the plain `max-w-2xl` rule, so the
            card used to render 384px-wide and tall. `!` wins that conflict;
            the `min()` clamps to the viewport gutters instead of relying on
            the base `max-w-[calc(100%-2rem)]`, which the important rule
            would otherwise shadow. 56rem (= `max-w-4xl`) matches the
            issue-detail body column so the card reads at the same width
            as the issue's own content. */}
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:!max-w-[min(56rem,calc(100%_-_2rem))]">
          <DialogHeader>
            <DialogTitle className="pr-8">{decision.question}</DialogTitle>
          </DialogHeader>
          <DecisionCard decision={decision} />
        </DialogContent>
      </Dialog>
    </>
  );
}
