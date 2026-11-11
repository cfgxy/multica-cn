"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import type { AgentRuntime } from "@multica/core/types";
import {
  runtimeDisplayLabel,
  useRemoveRuntimeFromList,
} from "@multica/core/runtimes";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

export function RemoveRuntimeFromListDialog({
  open,
  onOpenChange,
  runtime,
  wsId,
  onRemoved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  runtime: AgentRuntime;
  wsId: string;
  onRemoved: () => void;
}) {
  const { t } = useT("runtimes");
  const removeRuntime = useRemoveRuntimeFromList(wsId);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) setSubmitting(false);
  }, [open]);

  const handleOpenChange = (next: boolean) => {
    if (!submitting) onOpenChange(next);
  };

  const handleConfirm = async () => {
    setSubmitting(true);
    try {
      await removeRuntime.mutateAsync(runtime.id);
      onRemoved();
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.detail.remove_from_list_dialog.error),
      );
    } finally {
      setSubmitting(false);
    }
  };

  const name = runtimeDisplayLabel(runtime);
  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
      <AlertDialogContent className="w-[calc(100vw-2rem)] !max-w-[440px] gap-0 overflow-hidden rounded-lg p-0">
        <div className="px-5 pb-4 pt-5">
          <AlertDialogTitle className="font-sans text-title-sm font-semibold">
            {t(($) => $.detail.remove_from_list_dialog.title, { name })}
          </AlertDialogTitle>
          <AlertDialogDescription className="mt-1 text-left text-body leading-5 text-pretty">
            {t(($) => $.detail.remove_from_list_dialog.description, { name })}
          </AlertDialogDescription>
        </div>
        <div className="border-t bg-muted/25 px-5 py-3">
          <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button
              type="button"
              variant="outline"
              className="w-full sm:w-auto"
              onClick={() => handleOpenChange(false)}
              disabled={submitting}
            >
              {t(($) => $.detail.remove_from_list_dialog.cancel)}
            </Button>
            <Button
              type="button"
              variant="destructive"
              className="w-full sm:w-auto"
              onClick={handleConfirm}
              disabled={submitting}
            >
              {submitting
                ? t(($) => $.detail.remove_from_list_dialog.submitting)
                : t(($) => $.detail.remove_from_list_dialog.confirm)}
            </Button>
          </div>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}
