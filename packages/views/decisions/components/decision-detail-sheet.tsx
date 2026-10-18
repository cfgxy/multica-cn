"use client";

import { ArrowUpRight } from "lucide-react";
import type { WorkspaceDecisionInboxItem } from "@multica/core/types";
import { DECISION_ANCHOR_PREFIX, useWorkspacePaths } from "@multica/core/paths";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { DecisionContextView } from "../../issues/components/decision-context-view";

export function DecisionDetailSheet({
  item,
  open,
  onOpenChange,
}: {
  item: WorkspaceDecisionInboxItem | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("decisions");
  const paths = useWorkspacePaths();
  if (!item) return null;

  const issuePath = paths.issueDetail(item.issue_id);
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full gap-0 overflow-hidden p-0 sm:max-w-xl"
        data-testid="decision-detail-sheet"
      >
        <SheetHeader className="shrink-0 border-b border-surface-border pr-12">
          <p className="text-caption text-muted-foreground">
            {item.issue_identifier || `#${item.issue_number}`} · {item.issue_title}
          </p>
          <SheetTitle className="break-words pr-2">{item.question}</SheetTitle>
          <SheetDescription>{t(($) => $.detail.read_only)}</SheetDescription>
        </SheetHeader>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
          {item.context ? (
            <DecisionContextView context={item.context} />
          ) : (
            <p className="text-caption text-muted-foreground">{t(($) => $.detail.no_context)}</p>
          )}

          <section className="space-y-2" aria-labelledby="decision-detail-options">
            <h3 id="decision-detail-options" className="text-caption font-semibold">
              {t(($) => $.detail.options)}
            </h3>
            <ol className="space-y-2">
              {item.options.map((option, index) => {
                const recommended = item.recommended_indices.includes(index);
                const selected = item.selected_indices.includes(index);
                return (
                  <li key={`${option.label}-${index}`} data-testid={`decision-detail-option-${index}`} className="flex min-w-0 items-start gap-2 rounded-md border border-surface-border p-3">
                    <span className="shrink-0 font-medium text-muted-foreground">{String.fromCharCode(65 + index)}</span>
                    <span className="min-w-0 flex-1 break-words">{option.label}</span>
                    {recommended && <span className="text-caption text-brand">{t(($) => $.detail.recommended)}</span>}
                    {selected && <span className="text-caption text-foreground">{t(($) => $.detail.selected)}</span>}
                  </li>
                );
              })}
            </ol>
            <p className="text-caption text-muted-foreground">
              {item.multi_select ? t(($) => $.detail.multi_select) : t(($) => $.detail.single_select)}
            </p>
          </section>
        </div>

        <SheetFooter className="shrink-0 border-t border-surface-border">
          <AppLink
            href={`${issuePath}#${DECISION_ANCHOR_PREFIX}${item.id}`}
            onClick={() => onOpenChange(false)}
            data-testid="decision-detail-open-task"
            className="inline-flex w-full items-center justify-center gap-2 rounded-md bg-brand px-3 py-2 text-body font-medium text-white hover:bg-brand/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand"
          >
            {t(($) => $.detail.open_task)}
            <ArrowUpRight className="size-4" aria-hidden />
          </AppLink>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
