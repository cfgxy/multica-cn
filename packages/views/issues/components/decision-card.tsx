"use client";

import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CircleHelp, ListChecks, ShieldCheck } from "lucide-react";
import { Card } from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { api } from "@multica/core/api";
import { patchDecisionInCache, stripDecisionOptionLetterPrefix } from "@multica/core/issues/decisions";
import { decisionRequestKeys } from "@multica/core/issues/decision-requests";
import { issueKeys } from "@multica/core/issues/queries";
import type { IssueDecision } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT, useTimeAgo } from "../../i18n";

// Decision cards (RUYI-345): an agent-run's structured question rendered in
// the issue comment stream. Open cards take picks from human members; the
// server refuses agent callers, so the UI only needs to fail readably —
// the card is the gate, the server is the authority.
//
// Option rows render the same index letters as DecisionBatchBar (RUYI-588)
// and strip the label's embedded "A：" prefix so the letter shows once.

const OPTION_LETTERS = ["A", "B", "C", "D"] as const;

// Authorization cards (RUYI-630) share the issue thread with question cards
// but are a different face: two fixed buttons (approve/deny), no free picks,
// no cancel here (the creator and the workspace Owner manage lifecycle from
// the decision center's authorization section). The server still enforces
// tier, window, and CAS — the card only needs to fail readably.
function AuthorizationCard({ decision }: { decision: IssueDecision }) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
  const qc = useQueryClient();

  const creatorName = getActorName(decision.created_by_type, decision.created_by_id);
  const answeredName = decision.answered_by_type
    ? getActorName(decision.answered_by_type, decision.answered_by_id ?? "")
    : null;

  const invalidate = (updated: IssueDecision) => {
    patchDecisionInCache(qc, updated.issue_id, updated);
    // The decision-center rows mirror the group — flip them in the same tick.
    qc.invalidateQueries({ queryKey: decisionRequestKeys.all() });
  };

  const answer = useMutation({
    mutationFn: (index: 0 | 1) =>
      api.answerIssueDecision(decision.issue_id, decision.id, [index]),
    onSuccess: invalidate,
    onError: () => {
      qc.invalidateQueries({ queryKey: issueKeys.decisions(decision.issue_id) });
      toast.error(t(($) => $.decisions.auth_answer_failed));
    },
  });

  const isOpen = decision.status === "open";
  const optionLabel = (idx: 0 | 1) => {
    const named = idx === 0 ? decision.approve_label : decision.deny_label;
    if (named) return stripDecisionOptionLetterPrefix(named);
    const opt = decision.options[idx];
    if (opt?.label) return stripDecisionOptionLetterPrefix(opt.label);
    return t(($) =>
      idx === 0 ? $.decisions.auth_approve_default : $.decisions.auth_deny_default,
    );
  };

  return (
    <Card className="mx-2 border-brand/40 p-4" data-testid="decision-card" data-decision-kind="authorization" data-status={decision.status}>
      <div className="flex items-start gap-2">
        <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-brand" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-caption font-medium text-muted-foreground">
              {t(($) => $.decisions.auth_title)}
            </span>
            {decision.status === "cancelled" && (
              <Badge variant="outline">{t(($) => $.decisions.status_cancelled)}</Badge>
            )}
            {decision.auth_state === "approved" && (
              <Badge variant="secondary">{t(($) => $.decisions.auth_state_approved)}</Badge>
            )}
            {decision.auth_state === "denied" && (
              <Badge variant="outline">{t(($) => $.decisions.auth_state_denied)}</Badge>
            )}
            {decision.executed_at && !decision.execution_error && (
              <Badge variant="secondary">{t(($) => $.decisions.auth_state_executed)}</Badge>
            )}
            {decision.execution_error && (
              <Badge variant="destructive">{t(($) => $.decisions.auth_state_execute_failed)}</Badge>
            )}
          </div>
          <p className="mt-1 whitespace-pre-wrap break-words text-body font-medium">{decision.question}</p>
          <p className="mt-0.5 text-caption text-muted-foreground">
            {creatorName} · {timeAgo(decision.created_at)}
          </p>
        </div>
      </div>

      {isOpen && (
        <div className="mt-3 flex items-center gap-2">
          <Button
            size="sm"
            disabled={answer.isPending}
            data-testid="decision-auth-approve"
            onClick={() => answer.mutate(0)}
          >
            {optionLabel(0)}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={answer.isPending}
            data-testid="decision-auth-deny"
            onClick={() => answer.mutate(1)}
          >
            {optionLabel(1)}
          </Button>
        </div>
      )}

      {decision.status === "answered" && (
        <div className="mt-3 flex items-center gap-2 text-caption text-muted-foreground">
          <ListChecks className="h-3.5 w-3.5 shrink-0" aria-hidden />
          <span>
            <span className="font-medium text-foreground">{answeredName ?? ""}</span>
            {decision.answered_at ? ` · ${timeAgo(decision.answered_at)}` : ""}
          </span>
        </div>
      )}

      {decision.execution_error && (
        <p className="mt-2 break-words text-caption text-destructive">
          {t(($) => $.decisions.auth_execute_failed, { reason: decision.execution_error })}
        </p>
      )}
    </Card>
  );
}

export function DecisionCard({ decision }: { decision: IssueDecision }) {
  if (decision.decision_kind === "authorization") {
    return <AuthorizationCard decision={decision} />;
  }
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const { getActorName } = useActorName();
  const qc = useQueryClient();
  const [selected, setSelected] = useState<number[]>([]);
  const [submitting, setSubmitting] = useState(false);

  const creatorName = getActorName(decision.created_by_type, decision.created_by_id);
  const answeredName = decision.answered_by_type
    ? getActorName(decision.answered_by_type, decision.answered_by_id ?? "")
    : null;

  const recommended = useMemo(
    () => new Set(decision.recommended_indices),
    [decision.recommended_indices],
  );

  const toggle = (idx: number) => {
    if (decision.status !== "open" || submitting) return;
    setSelected((prev) => {
      if (prev.includes(idx)) return prev.filter((i) => i !== idx);
      return decision.multi_select ? [...prev, idx] : [idx];
    });
  };

  const invalidate = (updated: IssueDecision) => {
    patchDecisionInCache(qc, updated.issue_id, updated);
  };

  const answer = useMutation({
    mutationFn: () => api.answerIssueDecision(decision.issue_id, decision.id, selected),
    onSuccess: (updated) => {
      invalidate(updated);
      setSelected([]);
    },
    onError: () => {
      // 409 (someone answered/cancelled first) is the common race — refetch
      // so the card flips to its terminal state instead of sitting stale-open.
      qc.invalidateQueries({ queryKey: issueKeys.decisions(decision.issue_id) });
      toast.error(t(($) => $.decisions.answer_failed));
    },
    onSettled: () => setSubmitting(false),
  });

  const cancel = useMutation({
    mutationFn: () => api.cancelIssueDecision(decision.issue_id, decision.id),
    onSuccess: invalidate,
    onError: () => {
      qc.invalidateQueries({ queryKey: issueKeys.decisions(decision.issue_id) });
      toast.error(t(($) => $.decisions.cancel_failed));
    },
  });

  const isOpen = decision.status === "open";
  const canSubmit = isOpen && selected.length > 0 && !submitting && !answer.isPending;

  return (
    <Card className="mx-2 p-4" data-testid="decision-card" data-status={decision.status}>
      <div className="flex items-start gap-2">
        <CircleHelp className="mt-0.5 h-4 w-4 shrink-0 text-brand" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-caption font-medium text-muted-foreground">
              {t(($) => $.decisions.title)}
            </span>
            {decision.status === "answered" && (
              <Badge variant="secondary">{t(($) => $.decisions.status_answered)}</Badge>
            )}
            {decision.status === "cancelled" && (
              <Badge variant="outline">{t(($) => $.decisions.status_cancelled)}</Badge>
            )}
          </div>
          <p className="mt-1 whitespace-pre-wrap break-words text-body font-medium">{decision.question}</p>
          <p className="mt-0.5 text-caption text-muted-foreground">
            {creatorName} · {timeAgo(decision.created_at)}
          </p>
        </div>
      </div>

      <div
        className="mt-3 flex flex-col gap-1.5"
        role={decision.multi_select ? "group" : "radiogroup"}
        aria-label={decision.question}
      >
        {decision.options.map((opt, idx) => {
          const isSelected = isOpen
            ? selected.includes(idx)
            : decision.selected_indices.includes(idx);
          const interactive = isOpen && !submitting;
          return (
            <button
              key={idx}
              type="button"
              role={decision.multi_select ? "checkbox" : "radio"}
              aria-checked={isSelected}
              disabled={!interactive}
              onClick={() => toggle(idx)}
              data-testid={`decision-option-${idx}`}
              className={cn(
                "flex items-start gap-2 rounded-md border px-3 py-2 text-left text-body transition-colors",
                interactive && "hover:bg-accent/60 cursor-pointer",
                isSelected && "border-brand bg-accent/40",
                !isOpen && "opacity-70",
              )}
            >
              <span
                className={cn(
                  "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center border",
                  decision.multi_select ? "rounded-sm" : "rounded-full",
                  isSelected && "border-brand bg-brand",
                )}
                aria-hidden
              />
              <span
                className={cn(
                  "shrink-0 font-medium",
                  isSelected ? "text-brand" : "text-muted-foreground",
                )}
                aria-hidden
              >
                {OPTION_LETTERS[idx] ?? idx + 1}
              </span>
              <span className="min-w-0 flex-1 break-words">
                {stripDecisionOptionLetterPrefix(opt.label)}
              </span>
              {recommended.has(idx) && (
                <Badge variant="outline" className="shrink-0">
                  {t(($) => $.decisions.recommended)}
                </Badge>
              )}
            </button>
          );
        })}
      </div>

      {isOpen && (
        <div className="mt-3 flex items-center gap-2">
          <Button
            size="sm"
            disabled={!canSubmit}
            onClick={() => {
              setSubmitting(true);
              answer.mutate();
            }}
          >
            {answer.isPending || submitting
              ? t(($) => $.decisions.submitting)
              : decision.multi_select
                ? t(($) => $.decisions.submit_multi)
                : t(($) => $.decisions.submit_single)}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={cancel.isPending || submitting}
            onClick={() => cancel.mutate()}
          >
            {t(($) => $.decisions.cancel_card)}
          </Button>
        </div>
      )}

      {decision.status === "answered" && (
        <div className="mt-3 flex items-center gap-2 text-caption text-muted-foreground">
          <ListChecks className="h-3.5 w-3.5 shrink-0" aria-hidden />
          <span>
            <span className="font-medium text-foreground">{answeredName ?? ""}</span>
            {decision.answered_at ? ` · ${timeAgo(decision.answered_at)}` : ""}
          </span>
        </div>
      )}
    </Card>
  );
}
