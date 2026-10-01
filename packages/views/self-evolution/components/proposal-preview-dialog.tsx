"use client";

import { useEffect, useMemo, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { api, clientErrorMessage } from "@multica/core/api";
import type { PromptProposalPreview } from "@multica/core/types";
import { useT } from "../../i18n";
import { DiffView } from "./proposal-diff-view";

/**
 * The approval preview dialog — the injection-defense half of the approve
 * path (RUYI-305). Approval is only sent with `confirm_diff_previewed: true`
 * after this dialog rendered the sandbox full-carrier diffs for every id in
 * the batch and the reviewer ticked the confirmation; the server refuses the
 * write when the flag is missing either way.
 */
export function ProposalPreviewDialog({
  open,
  onOpenChange,
  ids,
  confirmLabel,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  ids: string[];
  confirmLabel: string;
  onConfirm: (confirmDiffPreviewed: boolean) => void;
}) {
  const { t } = useT("self-evolution");
  const [previews, setPreviews] = useState<PromptProposalPreview[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const [confirmed, setConfirmed] = useState(false);

  const key = useMemo(() => ids.join(","), [ids]);

  useEffect(() => {
    setConfirmed(false);
    setPreviews(null);
    setLoadError("");
    if (!open || ids.length === 0) return;
    let alive = true;
    Promise.all(ids.map((id) => api.previewPromptProposal(id)))
      .then((all) => {
        if (alive) setPreviews(all);
      })
      .catch((e: unknown) => {
        if (alive) setLoadError(clientErrorMessage(e) ?? t(($) => $.legislation.diffLoadError));
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, key]);

  const loaded = previews != null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto" data-testid="proposal-preview-dialog">
        <DialogHeader>
          <DialogTitle>{t(($) => $.legislation.previewTitle)}</DialogTitle>
          <DialogDescription>{t(($) => $.legislation.previewDescription)}</DialogDescription>
        </DialogHeader>
        {loadError ? (
          <div className="text-destructive text-body" data-testid="proposal-preview-error">
            {loadError}
          </div>
        ) : !loaded ? (
          <div className="flex flex-col gap-3" data-testid="proposal-preview-loading">
            <div className="text-muted-foreground text-body">{t(($) => $.legislation.batchLoading)}</div>
            <Skeleton className="h-24 w-full" />
          </div>
        ) : (
          <div className="flex flex-col gap-4" data-testid="proposal-preview-diffs">
            {previews.map((p, i) => (
              <div key={p.proposal.id} className="grid gap-1">
                <div className="text-body font-medium">
                  {i + 1}. {p.proposal.clause_name}
                  <span className="text-muted-foreground ml-2 text-caption">
                    {t(($) => $.legislation.baselineUsed)}: {String(p.baseline_used)}
                  </span>
                </div>
                <DiffView diff={p.diff} />
                {p.diff.length === 0 ? (
                  <div className="text-muted-foreground text-caption">
                    {t(($) => $.legislation.diffEmpty)}
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        )}
        <div className="flex items-center gap-2">
          <Checkbox
            id="proposal-preview-confirmed"
            checked={confirmed}
            onCheckedChange={(v) => setConfirmed(v === true)}
            disabled={!loaded}
          />
          <Label htmlFor="proposal-preview-confirmed">
            {t(($) => $.legislation.confirmPreviewed)}
          </Label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.legislation.cancel)}
          </Button>
          <Button
            data-testid="proposal-preview-confirm"
            disabled={!loaded || !confirmed}
            onClick={() => onConfirm(true)}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
