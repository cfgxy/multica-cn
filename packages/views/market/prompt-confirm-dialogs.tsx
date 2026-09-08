"use client";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../i18n";

/**
 * Withdrawal confirmation.
 *
 * The description spells out what a withdrawal does and does not do, because
 * the two halves pull in opposite directions: nobody can install or apply the
 * version again, yet every workspace that already applied it keeps the text.
 * A user who reads "withdraw" as "recall" would be wrong in a way that matters
 * — and the action itself has no undo.
 */
export function PromptWithdrawDialog({
  open,
  version,
  pending,
  onOpenChange,
  onConfirm,
}: {
  open: boolean;
  version: number | null;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  const { t } = useT("prompt-market");
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t(($) => $.withdraw.title, { version: version ?? 1 })}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.withdraw.description)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>
            {t(($) => $.withdraw.cancel)}
          </AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={pending} onClick={onConfirm}>
            {t(($) => $.withdraw.confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

/**
 * Restore confirmation.
 *
 * One step, not a stack — the copy says so, so nobody treats this as an undo
 * history. When the prompt has been hand-edited since the apply the server
 * refuses; the caller is expected not to render the entry point at all in that
 * case, and the refusal is the second line of defence rather than the first.
 */
export function PromptRestoreDialog({
  open,
  pending,
  onOpenChange,
  onConfirm,
}: {
  open: boolean;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  const { t } = useT("prompt-market");
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.restore.title)}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.restore.description)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>
            {t(($) => $.restore.cancel)}
          </AlertDialogCancel>
          <AlertDialogAction disabled={pending} onClick={onConfirm}>
            {t(($) => $.restore.confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
