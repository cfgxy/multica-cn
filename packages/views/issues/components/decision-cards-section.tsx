"use client";

import { useState } from "react";
import { ChevronLeft, ChevronRight, ListChecks } from "lucide-react";
import type { IssueDecision } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";
import { DecisionCard } from "./decision-card";

export function DecisionCardsSection({ decisions }: { decisions: IssueDecision[] }) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = decisions.find((decision) => decision.id === selectedId) ?? null;

  return (
    <>
      <button
        type="button"
        className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-caption font-medium transition-colors mb-2 hover:bg-accent/70"
        onClick={() => { setSelectedId(null); setOpen(true); }}
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
            <DialogTitle>{selected ? selected.question : t(($) => $.decisions.section_all)}</DialogTitle>
          </DialogHeader>
          {selected ? (
            <div className="space-y-3">
              <Button variant="ghost" size="sm" onClick={() => setSelectedId(null)}>
                <ChevronLeft />{t(($) => $.decisions.back_to_list)}
              </Button>
              <DecisionCard decision={selected} />
            </div>
          ) : (
            <div className="divide-y">
              {decisions.map((decision) => (
                <button
                  key={decision.id}
                  type="button"
                  className="flex w-full items-center gap-3 py-3 text-left hover:bg-accent/50"
                  onClick={() => setSelectedId(decision.id)}
                  data-testid={`decision-list-row-${decision.id}`}
                >
                  <span className="min-w-0 flex-1 truncate text-body">{decision.question}</span>
                  <Badge variant={decision.status === "open" ? "secondary" : "outline"}>
                    {decision.status === "open"
                      ? t(($) => $.decisions.status_open)
                      : decision.status === "answered"
                        ? t(($) => $.decisions.status_answered)
                        : t(($) => $.decisions.status_cancelled)}
                  </Badge>
                </button>
              ))}
              {decisions.length === 0 && <p className="py-6 text-center text-caption text-muted-foreground">{t(($) => $.decisions.empty_issue)}</p>}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
