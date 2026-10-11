"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { useMutation } from "@tanstack/react-query";
import { CirclePause, CirclePlay, X } from "lucide-react";
import {
  issueSuspensionsQueryOptions,
  suspendIssue,
  useReleaseSuspension,
} from "@multica/core/issues/suspensions";
import { workspaceDecisionInboxQueryOptions } from "@multica/core/issues/decisions";
import { useCurrentWorkspace } from "@multica/core/paths";
import type { Issue, WorkspaceDecisionInboxItem } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../../i18n";
import { IssuePickerModal } from "../../modals/issue-picker-modal";

// Suspension banner (RUYI-690): the issue detail's single surface for the
// suspension dimension. Three states, mutually exclusive:
//   active suspension — what it waits on + the resume condition, with
//                       resume/cancel actions;
//   blocked, not suspended — the 「转为挂起」 guidance: blocked freezes work
//                       inside the lifecycle; suspension expresses "my part
//                       is done, the wait is external" without touching
//                       status, and can auto-resume;
//   anything else      — nothing (negative contract: pre-RUYI-690 layout).

type WaitingType = "external" | "manual" | "issue" | "decision";

export function SuspensionBanner({
  issue,
  onChanged,
}: {
  issue: Issue;
  /** Refetch the issue after a mutation so `suspended_at` re-renders. */
  onChanged: () => Promise<unknown>;
}) {
  const { t } = useT("issues");
  const [dialogOpen, setDialogOpen] = useState(false);
  const { data: suspensions = [] } = useQuery({
    ...issueSuspensionsQueryOptions(issue.id),
    enabled: !!issue.suspended_at,
  });
  const active = suspensions.find((s) => s.status === "active");
  const resume = useReleaseSuspension("resume");
  const cancel = useReleaseSuspension("cancel");

  if (active) {
    return (
      <>
        <div
          className="flex flex-col gap-2 rounded-lg border border-violet-500/30 bg-violet-500/5 px-3 py-2.5"
          data-testid="suspension-banner-active"
        >
          <span className="flex min-w-0 items-center gap-2">
            <CirclePause className="size-4 shrink-0 text-violet-600 dark:text-violet-400" />
            <span className="text-caption font-medium text-violet-700 dark:text-violet-300">
              {t(($) => $.suspension.badge)}
            </span>
            <span className="truncate text-caption text-muted-foreground">
              {active.waiting_on_summary}
              {active.resume_condition
                ? ` · ${t(($) => $.suspension.banner.condition)}: ${active.resume_condition}`
                : ""}
            </span>
            <span className="ml-auto flex shrink-0 items-center gap-1">
              <Button
                variant="outline"
                size="sm"
                className="h-6 gap-1 px-2 text-caption"
                disabled={resume.isPending || cancel.isPending}
                data-testid="suspension-banner-resume"
                onClick={() =>
                  resume.mutate(
                    { issueId: issue.id, suspensionId: active.id },
                    { onSuccess: () => void onChanged() },
                  )
                }
              >
                <CirclePlay className="size-3" />
                {t(($) => $.suspension.banner.resume)}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="h-6 gap-1 px-2 text-caption text-muted-foreground"
                disabled={resume.isPending || cancel.isPending}
                data-testid="suspension-banner-cancel"
                onClick={() =>
                  cancel.mutate(
                    { issueId: issue.id, suspensionId: active.id },
                    { onSuccess: () => void onChanged() },
                  )
                }
              >
                <X className="size-3" />
                {t(($) => $.suspension.banner.cancel)}
              </Button>
            </span>
          </span>
          {active.reason && (
            <span className="pl-6 text-caption text-muted-foreground">
              {t(($) => $.suspension.banner.reason_label)}: {active.reason}
            </span>
          )}
        </div>
      </>
    );
  }

  if (issue.status === "blocked") {
    return (
      <>
        <div
          className="flex flex-col gap-2 rounded-lg border border-dashed border-violet-500/40 px-3 py-2.5 sm:flex-row sm:items-center"
          data-testid="suspension-banner-blocked-hint"
        >
          <p className="min-w-0 flex-1 text-caption text-muted-foreground">
            {t(($) => $.suspension.banner.blocked_hint)}
          </p>
          <Button
            variant="outline"
            size="sm"
            className="h-7 shrink-0 gap-1"
            data-testid="suspension-banner-convert"
            onClick={() => setDialogOpen(true)}
          >
            <CirclePause className="size-3.5" />
            {t(($) => $.suspension.banner.convert)}
          </Button>
        </div>
        <SuspendIssueDialog
          issue={issue}
          open={dialogOpen}
          onOpenChange={setDialogOpen}
          onChanged={onChanged}
        />
      </>
    );
  }

  return null;
}

/**
 * Suspend form (RUYI-690): reason + wait type + conditional target +
 * resume condition. The server derives the machine predicate from the
 * target (issue/decision) or requires free-text resume_condition
 * (external/manual) — the client mirrors that split so the submit is
 * right the first time.
 */
export function SuspendIssueDialog({
  issue,
  open,
  onOpenChange,
  onChanged,
}: {
  issue: Issue;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onChanged: () => Promise<unknown>;
}) {
  const { t } = useT("issues");
  const ws = useCurrentWorkspace();
  const [reason, setReason] = useState("");
  const [waitingType, setWaitingType] = useState<WaitingType>("external");
  const [targetIssue, setTargetIssue] = useState<Issue | null>(null);
  const [targetDecision, setTargetDecision] =
    useState<WorkspaceDecisionInboxItem | null>(null);
  const [condition, setCondition] = useState("");
  const [pickerOpen, setPickerOpen] = useState(false);

  const { data: openCards = [] } = useQuery({
    ...workspaceDecisionInboxQueryOptions(ws?.id),
    enabled: open && waitingType === "decision",
    select: (data) => data.items.filter((i) => i.status === "open"),
  });

  const mutation = useMutation({
    mutationFn: suspendIssue,
    onSuccess: async () => {
      toast.success(t(($) => $.suspension.banner.toast_success));
      onOpenChange(false);
      setReason("");
      setCondition("");
      setTargetIssue(null);
      setTargetDecision(null);
      setWaitingType("external");
      await onChanged();
    },
    onError: (err) =>
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.suspension.banner.toast_failed),
      ),
  });

  const needsCondition = waitingType === "external" || waitingType === "manual";
  const targetMissing =
    (waitingType === "issue" && !targetIssue) ||
    (waitingType === "decision" && !targetDecision);
  const canSubmit =
    reason.trim().length > 0 && !targetMissing && (!needsCondition || condition.trim().length > 0);

  const typeItems = (["external", "manual", "issue", "decision"] as const).map(
    (v) => ({
      value: v,
      label: t(($) => $.suspension.banner[`type_${v}` as const]),
    }),
  );

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-md" data-testid="suspend-issue-dialog">
          <DialogTitle>{t(($) => $.suspension.banner.dialog_title)}</DialogTitle>
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <Label>{t(($) => $.suspension.banner.reason_label)}</Label>
              <Input
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder={t(($) => $.suspension.banner.reason_placeholder)}
                data-testid="suspend-reason-input"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label>{t(($) => $.suspension.banner.waiting_type_label)}</Label>
              <Select
                items={typeItems}
                value={waitingType}
                onValueChange={(v) => {
                  if (v) setWaitingType(v as WaitingType);
                }}
              >
                <SelectTrigger data-testid="suspend-waiting-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {typeItems.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {waitingType === "issue" && (
              <div className="flex flex-col gap-1.5">
                <Label>{t(($) => $.suspension.banner.target_issue)}</Label>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-8 justify-start font-normal"
                  data-testid="suspend-target-issue"
                  onClick={() => setPickerOpen(true)}
                >
                  {targetIssue
                    ? `${targetIssue.identifier} ${targetIssue.title}`
                    : t(($) => $.suspension.banner.pick_issue)}
                </Button>
              </div>
            )}

            {waitingType === "decision" && (
              <div className="flex flex-col gap-1.5">
                <Label>{t(($) => $.suspension.banner.target_decision)}</Label>
                <Select
                  items={openCards.map((c) => ({
                    value: c.id,
                    label: `${c.issue_identifier} · ${c.question}`,
                  }))}
                  value={targetDecision?.id ?? ""}
                  onValueChange={(v) => {
                    const card = openCards.find((c) => c.id === v);
                    if (card) setTargetDecision(card);
                  }}
                >
                  <SelectTrigger data-testid="suspend-target-decision">
                    <SelectValue>
                      {targetDecision
                        ? `${targetDecision.issue_identifier} · ${targetDecision.question}`
                        : t(($) => $.suspension.banner.pick_decision)}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {openCards.length === 0 && (
                      <div className="px-2 py-1.5 text-caption text-muted-foreground">
                        {t(($) => $.suspension.banner.no_open_cards)}
                      </div>
                    )}
                    {openCards.map((card) => (
                      <SelectItem key={card.id} value={card.id}>
                        {`${card.issue_identifier} · ${card.question}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <Label>
                {t(($) => $.suspension.banner.condition_label)}
                {needsCondition && (
                  <span className="text-muted-foreground">
                    {" "}
                    ({t(($) => $.suspension.banner.condition_required_hint)})
                  </span>
                )}
              </Label>
              <Textarea
                value={condition}
                onChange={(e) => setCondition(e.target.value)}
                placeholder={t(($) => $.suspension.banner.condition_placeholder)}
                rows={2}
                data-testid="suspend-condition-input"
              />
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onOpenChange(false)}
              >
                {t(($) => $.suspension.banner.cancel_dialog)}
              </Button>
              <Button
                size="sm"
                disabled={!canSubmit || mutation.isPending}
                data-testid="suspend-submit"
                onClick={() =>
                  mutation.mutate({
                    issueId: issue.id,
                    reason: reason.trim(),
                    waiting_on_type: waitingType,
                    ...(waitingType === "issue" && targetIssue
                      ? { waiting_on_issue_id: targetIssue.id }
                      : {}),
                    ...(waitingType === "decision" && targetDecision
                      ? { waiting_on_decision_id: targetDecision.id }
                      : {}),
                    ...(condition.trim()
                      ? { resume_condition: condition.trim() }
                      : {}),
                  })
                }
              >
                {t(($) => $.suspension.banner.submit)}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
      <IssuePickerModal
        open={pickerOpen}
        onOpenChange={setPickerOpen}
        title={t(($) => $.suspension.banner.target_issue)}
        description={t(($) => $.suspension.banner.pick_issue)}
        excludeIds={[issue.id]}
        onSelect={(selected) => {
          setTargetIssue(selected);
          setPickerOpen(false);
        }}
      />
    </>
  );
}
