"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import type { QuizVerdict, QuizView } from "@multica/core/self-evolution";
import { useT, useLocale } from "../../i18n";

/**
 * The regression reading for one prompt scope (RUYI-185).
 *
 * Four states, and only the last one prints a verdict. The three others say
 * what is missing — no version, not enough repeats, no previous version to
 * compare against — because a quiz that has not run yet must never render as
 * "steady": the label a reader trusts least is the one that appears by default.
 *
 * The card is a reading and says so in its own copy. Nothing on the publish
 * surface links to it, and `server/internal/handler/prompt_quiz_publish_test.go`
 * holds the server side of the same rule.
 *
 * The verdict matrix itself is covered canonically in
 * `packages/core/self-evolution/quiz.test.ts`; this component owns the wiring.
 */
export function QuizReadingCard({ view }: { view: QuizView }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const num = (v: number): string =>
    new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(v);

  return (
    <Card className="gap-3" data-testid="quiz-reading-card">
      <CardHeader className="gap-1">
        <CardTitle className="flex items-center gap-2 text-body font-medium">
          {t(($) => $.quiz.reading.title)}
          {view.state === "compared" ? (
            <Badge variant={verdictVariant(view.verdict)} data-testid="quiz-verdict">
              {t(($) => $.quiz.reading.verdict[view.verdict])}
            </Badge>
          ) : null}
        </CardTitle>
        <CardDescription className="text-caption">
          {t(($) => $.quiz.reading.description)}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {view.state === "no_version" ? (
          <p className="text-caption text-muted-foreground" data-testid="quiz-state-no-version">
            {t(($) => $.quiz.reading.noVersion)}
          </p>
        ) : view.state === "accumulating" ? (
          // Two counts, never their ratio and never a bare percentage: "7 of 12
          // runs" is the only form in which a partial sample cannot be mistaken
          // for a measured value.
          <p className="text-caption text-muted-foreground" data-testid="quiz-state-accumulating">
            {t(($) => $.quiz.reading.accumulating, {
              version: view.version,
              have: view.have,
              need: view.need,
            })}
          </p>
        ) : view.state === "no_baseline" ? (
          <p className="text-caption text-muted-foreground" data-testid="quiz-state-no-baseline">
            {t(($) => $.quiz.reading.noBaseline, {
              version: view.version,
              count: view.current.n,
              median: num(view.current.median),
              iqr: num(view.current.iqr),
            })}
          </p>
        ) : (
          <>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.quiz.reading.versions, {
                baseline: view.baselineVersion,
                current: view.version,
              })}
            </p>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-caption">
              <QuizStat
                label={t(($) => $.quiz.reading.stats.baseline)}
                value={t(($) => $.quiz.reading.stats.group, {
                  median: num(view.comparison.baseline.median),
                  iqr: num(view.comparison.baseline.iqr),
                  n: view.comparison.baseline.n,
                })}
              />
              <QuizStat
                label={t(($) => $.quiz.reading.stats.current)}
                value={t(($) => $.quiz.reading.stats.group, {
                  median: num(view.comparison.current.median),
                  iqr: num(view.comparison.current.iqr),
                  n: view.comparison.current.n,
                })}
              />
              {/* Both sides of the decision, so the label can be recomputed
                  rather than trusted. */}
              <QuizStat
                label={t(($) => $.quiz.reading.stats.statistic)}
                value={new Intl.NumberFormat(locale, {
                  maximumFractionDigits: 2,
                  signDisplay: "exceptZero",
                }).format(view.comparison.z)}
              />
              <QuizStat
                label={t(($) => $.quiz.reading.stats.threshold)}
                value={new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(
                  view.comparison.threshold,
                )}
              />
            </dl>
          </>
        )}

        {view.state !== "no_version" && (view.outcomes.errored ?? 0) > 0 ? (
          <p className="text-caption text-muted-foreground" data-testid="quiz-errored">
            {t(($) => $.quiz.reading.errored, { count: view.outcomes.errored ?? 0 })}
          </p>
        ) : null}
        <p className="text-caption text-muted-foreground">{t(($) => $.quiz.reading.notAGate)}</p>
      </CardContent>
    </Card>
  );
}

function QuizStat({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="tabular-nums">{value}</dd>
    </>
  );
}

/**
 * The verdict's badge variant.
 *
 * A regression is `outline` rather than `destructive`: the quiz does not block
 * anything, and a red badge on a reading invites the reader to treat it as one.
 */
function verdictVariant(verdict: QuizVerdict): "secondary" | "outline" {
  return verdict === "steady" ? "secondary" : "outline";
}
