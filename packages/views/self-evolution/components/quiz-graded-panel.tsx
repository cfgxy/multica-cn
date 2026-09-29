"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { clientErrorMessage } from "@multica/core/api";
import {
  promptQuizBatchOptions,
  promptQuizSamplesOptions,
  useCreatePromptQuizBatch,
} from "@multica/core/self-evolution";
import type { PromptQuizBaseline, PromptQuizSampleRow } from "@multica/core/types";
import { useT, useLocale } from "../../i18n";

/**
 * The graded side of the quiz (RUYI-286), owner-only like the rubrics it
 * reflects: the per-item mean summary from the baseline, a button that orders
 * real quiz runs through the same agent_task_queue path the sweep uses, and
 * the newest graded samples with their per-assertion evidence.
 *
 * The same honesty rule as the reading card applies to scores: "not graded"
 * is a state, never zero. A group with `graded: 0` renders the un-graded
 * label, because a 0% that came from "nothing was graded yet" reads as a
 * measured failure — the exact mistake this panel exists to prevent.
 *
 * score_detail carries what the grader observed (which phrase or pattern a
 * check matched). It is the grading output of the private half of an item,
 * which is why this panel is gated on `canManage` at its mount point and the
 * server gates the endpoints behind the owner role.
 */
export function QuizGradedPanel({
  wsId,
  agentId,
  baseline,
}: {
  wsId: string;
  agentId: string;
  baseline: PromptQuizBaseline;
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const pct = (v: number): string =>
    new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0 }).format(v);

  const run = useCreatePromptQuizBatch(wsId);
  const [batchId, setBatchId] = useState("");
  const batch = useQuery(promptQuizBatchOptions(wsId, batchId));
  const samples = useQuery(promptQuizSamplesOptions(wsId, "agent", agentId));

  const scores = baseline.scores;
  const graded = scores?.graded ?? 0;

  const orderRuns = () => {
    run.mutate(
      { agent_ids: [agentId] },
      {
        onSuccess: (resp) => {
          if (resp.batch_id !== "") setBatchId(resp.batch_id);
          const refused = resp.refused_agents?.length ?? 0;
          if (refused > 0) {
            toast.warning(t(($) => $.quiz.graded.refused, { count: refused }));
          } else {
            toast.success(t(($) => $.quiz.graded.ordered, { ordered: resp.ordered }));
          }
        },
        onError: (e) =>
          toast.error(clientErrorMessage(e) ?? t(($) => $.quiz.graded.runError)),
      },
    );
  };

  return (
    <Card className="gap-3" data-testid="quiz-graded-panel">
      <CardHeader className="gap-1">
        <CardTitle className="text-body font-medium">{t(($) => $.quiz.graded.title)}</CardTitle>
        <CardDescription className="text-caption">
          {t(($) => $.quiz.graded.description)}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          {graded > 0 && scores !== undefined ? (
            <Badge variant="secondary" data-testid="quiz-graded-summary">
              {t(($) => $.quiz.graded.summary, { graded, mean: pct(scores.mean) })}
            </Badge>
          ) : (
            <Badge variant="outline" data-testid="quiz-graded-ungraded">
              {t(($) => $.quiz.graded.ungraded)}
            </Badge>
          )}
          <Button
            size="sm"
            variant="outline"
            disabled={run.isPending}
            onClick={orderRuns}
            data-testid="quiz-graded-run"
          >
            {t(($) => $.quiz.graded.run)}
          </Button>
        </div>

        {batchId !== "" ? (
          batch.isPending ? (
            <Skeleton className="h-16 w-full rounded-lg" />
          ) : batch.isError ? (
            <p className="text-caption text-muted-foreground">{t(($) => $.quiz.reading.error)}</p>
          ) : (
            <div className="flex flex-col gap-1" data-testid="quiz-graded-batch">
              <p className="text-caption text-muted-foreground">
                {t(($) => $.quiz.graded.batchLines, {
                  answered: batch.data?.counts.answered ?? 0,
                  errored: batch.data?.counts.errored ?? 0,
                  graded: batch.data?.scores?.graded ?? 0,
                })}
              </p>
            </div>
          )
        ) : null}

        <div className="flex flex-col gap-1">
          <h4 className="text-caption font-medium">{t(($) => $.quiz.graded.samplesTitle)}</h4>
          {samples.isPending ? (
            <Skeleton className="h-16 w-full rounded-lg" />
          ) : samples.isError ? (
            <p className="text-caption text-muted-foreground">{t(($) => $.quiz.reading.error)}</p>
          ) : (samples.data ?? []).length === 0 ? (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.quiz.graded.samplesEmpty)}
            </p>
          ) : (
            <ul className="flex flex-col gap-2">
              {(samples.data ?? []).slice(0, 10).map((row) => (
                <SampleRow key={row.task_id} row={row} pct={pct} />
              ))}
            </ul>
          )}
        </div>

        <p className="text-caption text-muted-foreground">{t(($) => $.quiz.graded.notAGate)}</p>
      </CardContent>
    </Card>
  );
}

/**
 * One graded sample: what ran, whether it was graded, and on what evidence.
 * A null score renders as the un-graded label — same rule as the summary.
 */
function SampleRow({
  row,
  pct,
}: {
  row: PromptQuizSampleRow;
  pct: (v: number) => string;
}) {
  const { t } = useT("self-evolution");
  const checks = parseChecks(row.score_detail);

  return (
    <li className="flex flex-col gap-1 rounded-md border border-border px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-caption font-medium">{row.item_title ?? row.item_slug ?? row.item_id}</span>
        <Badge variant="outline">{t(($) => $.quiz.graded.version, { version: row.version })}</Badge>
        <Badge variant={row.outcome === "answered" ? "secondary" : "outline"}>
          {row.outcome === "answered"
            ? t(($) => $.quiz.graded.answered)
            : t(($) => $.quiz.graded.errored)}
        </Badge>
        {row.score !== null && row.score !== undefined ? (
          <Badge variant="secondary" data-testid="quiz-sample-score">
            {pct(row.score)}
          </Badge>
        ) : (
          <Badge variant="outline">{t(($) => $.quiz.graded.ungraded)}</Badge>
        )}
      </div>
      {checks.length > 0 ? (
        <ul className="flex flex-col gap-0.5">
          {checks.map((check, i) => (
            <li
              key={`${check.id}-${i}`}
              className="text-caption text-muted-foreground"
              data-testid={`quiz-check-${check.passed ? "passed" : "failed"}`}
            >
              {check.passed
                ? t(($) => $.quiz.graded.checkPassed, {
                    id: check.id,
                    kind: check.kind,
                    evidence: check.evidence,
                  })
                : t(($) => $.quiz.graded.checkFailed, {
                    id: check.id,
                    kind: check.kind,
                    evidence: check.evidence,
                  })}
            </li>
          ))}
        </ul>
      ) : null}
    </li>
  );
}

interface ParsedCheck {
  id: string;
  kind: string;
  passed: boolean;
  evidence: string;
}

/**
 * `score_detail` is server JSON parsed as unknown; the grader's own contract
 * (pkg/promptquiz CheckVerdict) is the shape, and anything else degrades to
 * "no detail shown" rather than to a fabricated row.
 */
function parseChecks(detail: unknown): ParsedCheck[] {
  if (!Array.isArray(detail)) return [];
  const out: ParsedCheck[] = [];
  for (const entry of detail) {
    if (entry === null || typeof entry !== "object") continue;
    const rec = entry as Record<string, unknown>;
    if (typeof rec.id !== "string" || typeof rec.kind !== "string") continue;
    if (typeof rec.passed !== "boolean") continue;
    out.push({
      id: rec.id,
      kind: rec.kind,
      passed: rec.passed,
      evidence: typeof rec.evidence === "string" ? rec.evidence : "",
    });
  }
  return out;
}
