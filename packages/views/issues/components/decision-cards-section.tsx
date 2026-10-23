"use client";

import { useState } from "react";
import { ChevronRight, ListChecks } from "lucide-react";
import type { IssueDecision } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { DecisionCardBrowser } from "./decision-card-browser";

// RUYI-863: the sidebar's "decision cards" entry is the more-menu entry of
// the unified browser — it opens on the FIRST card under the default "all"
// filter (list order = creation order), and the browser itself carries the
// status filter, x/N position and previous/next navigation. It no longer
// keeps its own list→detail selection state: the card view is the shared
// DecisionCard, rendered through the same browser every entry uses.

export function DecisionCardsSection({ decisions }: { decisions: IssueDecision[] }) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);

  return (
    <>
      <button
        type="button"
        className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors mb-2 hover:bg-accent/70"
        onClick={() => setOpen(true)}
        data-testid="decision-cards-sidebar-entry"
      >
        <ListChecks className="mr-1 size-3.5" aria-hidden />
        {t(($) => $.decisions.section_all)}
        <Badge variant="secondary" className="ml-auto">{decisions.length}</Badge>
        <ChevronRight className="!size-3 shrink-0 text-muted-foreground" aria-hidden />
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t(($) => $.decisions.section_all)}</DialogTitle>
          </DialogHeader>
          <DecisionCardBrowser decisions={decisions} initialFilter="all" />
        </DialogContent>
      </Dialog>
    </>
  );
}
