"use client";

import { useQuery } from "@tanstack/react-query";
import { Check, Minus, X } from "lucide-react";
import type {
  ExecutionProfileActivationResponse,
  ExecutionProfileActivationResult,
} from "@multica/core/types/execution-profile";
import { agentListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";

/**
 * Per-member outcome report for a profile activation (RUYI-57, F5).
 *
 * Only opens when something did not apply. A bulk write that partly succeeded
 * is the case a toast cannot honestly summarise: the user has to know WHICH
 * members still run the old configuration, because those are the ones whose
 * tokens are still coming from the provider they were trying to leave.
 */
export function ExecutionProfileResultDialog({
  wsId,
  result,
  onClose,
  onRetry,
}: {
  wsId: string;
  result: ExecutionProfileActivationResponse | null;
  onClose: () => void;
  onRetry?: () => void;
}) {
  const { t } = useT("squads");
  const agentsQuery = useQuery({ ...agentListOptions(wsId), enabled: !!result });
  const nameOf = (agentId: string) =>
    agentsQuery.data?.find((a) => a.id === agentId)?.name ?? agentId;

  if (!result) return null;

  // applied === 0 means the server wrote nothing at all and left the active
  // pointer where it was, so the dialog leads with that instead of a count.
  const nothingApplied = result.applied === 0;

  return (
    <Dialog open onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {nothingApplied
              ? t(($) => $.execution_profile.result_failed_title)
              : t(($) => $.execution_profile.result_partial_title)}
          </DialogTitle>
          <DialogDescription>
            {nothingApplied
              ? t(($) => $.execution_profile.result_failed_description)
              : t(($) => $.execution_profile.result_partial_description, {
                  applied: result.applied,
                  total: result.results.length,
                })}
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-[320px] overflow-y-auto rounded-md border divide-y">
          {result.results.map((row) => (
            <ResultRow key={row.agent_id} row={row} name={nameOf(row.agent_id)} />
          ))}
        </div>

        <DialogFooter>
          {nothingApplied && onRetry && (
            <Button variant="outline" onClick={onRetry}>
              {t(($) => $.execution_profile.retry)}
            </Button>
          )}
          <Button onClick={onClose}>
            {t(($) => $.execution_profile.close)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ResultRow({
  row,
  name,
}: {
  row: ExecutionProfileActivationResult;
  name: string;
}) {
  const { t } = useT("squads");
  // Reason codes are machine-readable; an unknown one still has to say
  // something, so it falls back to the raw code rather than rendering blank.
  const reasonText =
    row.status === "applied"
      ? null
      : row.reason === "agent_not_found"
        ? t(($) => $.execution_profile.reason_agent_not_found)
        : row.reason === "agent_archived"
          ? t(($) => $.execution_profile.reason_agent_archived)
          : row.reason === "runtime_unavailable"
            ? t(($) => $.execution_profile.reason_runtime_unavailable)
            : row.reason === "thinking_level_unsupported"
              ? t(($) => $.execution_profile.reason_thinking_unsupported)
              : row.reason === "update_failed"
                ? t(($) => $.execution_profile.reason_update_failed)
                : (row.reason ?? null);

  const icon =
    row.status === "applied" ? (
      <Check className="size-3.5 text-success" />
    ) : row.status === "skipped" ? (
      <Minus className="size-3.5 text-muted-foreground" />
    ) : (
      <X className="size-3.5 text-destructive" />
    );

  return (
    <div className="flex items-start gap-2 px-3 py-2">
      <span className="mt-0.5 shrink-0">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body">{name}</span>
        {reasonText && (
          <span className="block text-caption text-muted-foreground">
            {reasonText}
          </span>
        )}
      </span>
    </div>
  );
}
