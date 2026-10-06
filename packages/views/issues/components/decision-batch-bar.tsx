"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ListChecks } from "lucide-react";
import { Card } from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { api } from "@multica/core/api";
import { patchDecisionInCache } from "@multica/core/issues/decisions";
import { issueKeys } from "@multica/core/issues/queries";
import type { IssueDecision } from "@multica/core/types";
import { useT } from "../../i18n";

// Batch answer bar (RUYI-471): shown above the first open card when an issue
// has two or more. One pass of picks, one submit, one shared echo comment —
// the compact text "1A 2B" the server numbers cards by is this bar's order
// (created_at ASC). Single-card answering stays on DecisionCard untouched.

const OPTION_LETTERS = ["A", "B", "C", "D"] as const;

export function DecisionBatchBar({ issueId, open }: { issueId: string; open: IssueDecision[] }) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const [picks, setPicks] = useState<Record<string, number[]>>({});
  const [submitting, setSubmitting] = useState(false);

  const toggle = (decision: IssueDecision, idx: number) => {
    if (submitting) return;
    setPicks((prev) => {
      const cur = prev[decision.id] ?? [];
      let next: number[];
      if (cur.includes(idx)) {
        next = cur.filter((i) => i !== idx);
      } else {
        next = decision.multi_select ? [...cur, idx].sort((a, b) => a - b) : [idx];
      }
      return { ...prev, [decision.id]: next };
    });
  };

  const answeredCount = open.filter((d) => (picks[d.id]?.length ?? 0) > 0).length;
  const canSubmit = answeredCount > 0 && !submitting;

  const submit = async () => {
    setSubmitting(true);
    try {
      const answers = open
        .filter((d) => (picks[d.id]?.length ?? 0) > 0)
        .map((d) => ({ decision_id: d.id, selected_indices: picks[d.id]! }));
      const resp = await api.answerIssueDecisionsBatch(issueId, answers);
      for (const r of resp.results) {
        if (r.status === "answered" && r.decision) {
          patchDecisionInCache(qc, issueId, r.decision);
        }
      }
      const failed = resp.results.filter((r) => r.status !== "answered");
      if (failed.length > 0) {
        // Per-card failures never roll the batch back — refetch so losing
        // cards flip to whoever won the race, then say the submit failed.
        qc.invalidateQueries({ queryKey: issueKeys.decisions(issueId) });
        toast.error(t(($) => $.decisions.answer_failed));
      }
      setPicks({});
    } catch {
      qc.invalidateQueries({ queryKey: issueKeys.decisions(issueId) });
      toast.error(t(($) => $.decisions.answer_failed));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Card className="mx-2 p-4" data-testid="decision-batch-bar">
      <div className="flex items-center gap-2">
        <ListChecks className="h-4 w-4 shrink-0 text-brand" aria-hidden />
        <span className="text-caption font-medium text-muted-foreground">
          {t(($) => $.decisions.batch_title)}
        </span>
        <Badge variant="secondary" data-testid="decision-batch-count">
          {open.length}
        </Badge>
      </div>

      <div className="mt-3 flex flex-col gap-3">
        {open.map((decision, cardIdx) => (
          <div key={decision.id} data-testid={`decision-batch-card-${decision.id}`}>
            <p className="break-words text-body font-medium">
              <span className="mr-1 text-muted-foreground">{cardIdx + 1}.</span>
              {decision.question}
            </p>
            <div
              className="mt-1.5 flex flex-wrap gap-1.5"
              role={decision.multi_select ? "group" : "radiogroup"}
              aria-label={decision.question}
            >
              {decision.options.map((opt, idx) => {
                const isSelected = (picks[decision.id] ?? []).includes(idx);
                return (
                  <button
                    key={idx}
                    type="button"
                    role={decision.multi_select ? "checkbox" : "radio"}
                    aria-checked={isSelected}
                    disabled={submitting}
                    onClick={() => toggle(decision, idx)}
                    data-testid={`decision-batch-option-${decision.id}-${idx}`}
                    className={cn(
                      "flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-left text-caption transition-colors",
                      !submitting && "hover:bg-accent/60 cursor-pointer",
                      isSelected && "border-brand bg-accent/40",
                    )}
                  >
                    <span
                      className={cn(
                        "font-medium",
                        isSelected ? "text-brand" : "text-muted-foreground",
                      )}
                      aria-hidden
                    >
                      {OPTION_LETTERS[idx] ?? idx + 1}
                    </span>
                    <span className="break-words">{opt.label}</span>
                  </button>
                );
              })}
            </div>
          </div>
        ))}
      </div>

      <div className="mt-3">
        <Button size="sm" disabled={!canSubmit} onClick={() => void submit()} data-testid="decision-batch-submit">
          {submitting
            ? t(($) => $.decisions.submitting)
            : t(($) => $.decisions.batch_submit)}
        </Button>
      </div>
    </Card>
  );
}
