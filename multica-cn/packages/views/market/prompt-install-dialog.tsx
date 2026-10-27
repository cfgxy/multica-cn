"use client";

import { Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import type { PromptMarketItem } from "@multica/core/types";
import { useT } from "../i18n";
import { licenseLabel } from "./prompt-market-labels";

/**
 * Confirms adding a version to the workspace library.
 *
 * The dialog exists mostly to say what installing does NOT do. Every other
 * install surface in the product changes something immediately, so a prompt
 * install that silently rewrites an agent is the mistake a user would
 * reasonably expect — the description states plainly that no agent and no
 * squad is touched until a separate apply.
 */
export function PromptInstallDialog({
  open,
  item,
  installing,
  onOpenChange,
  onConfirm,
}: {
  open: boolean;
  item: PromptMarketItem | null;
  installing: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  const { t } = useT("prompt-market");

  if (!item) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.install.title, { name: item.name })}</DialogTitle>
          <DialogDescription>{t(($) => $.install.description)}</DialogDescription>
        </DialogHeader>

        <dl className="grid gap-x-6 gap-y-2 text-caption sm:grid-cols-2">
          <div className="min-w-0">
            <dt className="text-muted-foreground">{t(($) => $.detail.publisher_label)}</dt>
            <dd className="truncate text-body">{item.publisher_display_name}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-muted-foreground">{t(($) => $.detail.license)}</dt>
            <dd className="truncate text-body">{licenseLabel(t, item.license_code)}</dd>
          </div>
        </dl>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={installing}
          >
            {t(($) => $.install.cancel)}
          </Button>
          <Button onClick={onConfirm} disabled={installing}>
            {installing ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
            {t(($) => $.install.confirm)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
