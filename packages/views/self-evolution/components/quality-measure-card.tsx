"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@multica/ui/components/ui/collapsible";
import type { MeasureView } from "@multica/core/self-evolution";
import type { PromptQualityDeduction, PromptQualityDimension } from "@multica/core/types";
import { useT, useLocale } from "../../i18n";
import { formatMeasureValue } from "./quality-format";

/**
 * One of the seven dimension cards (RUYI-184).
 *
 * The three states render three different things and only the `ok` branch ever
 * prints a number. There is no `?? 0` anywhere below: "no data" and "sample
 * too small" are outcomes the reader has to see as themselves, because a 0 on
 * a discipline or failure-rate card reads as a finding about the prompt
 * (T5/T7).
 */
export function QualityMeasureCard({
  dimension,
  view,
  runs,
  deductions,
}: {
  dimension: PromptQualityDimension;
  view: MeasureView;
  /** The version group's finished runs. D2 states its sample against it. */
  runs?: number;
  /** D2 only: the recorded breaches behind the median (RUYI-287). */
  deductions?: PromptQualityDeduction[];
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();

  const label = t(($) => $.quality.dimensions[dimension].label);
  const hint = t(($) => $.quality.dimensions[dimension].hint);

  // D2's sample is the covered count, not the run count. Naming both sides is
  // what keeps a 36-of-40 window from reading as fully inspected — the gap
  // between the two numbers is runs no rule could read, not consent to skip.
  const coverage =
    dimension === "discipline" && runs !== undefined ? (
      <div className="text-caption text-muted-foreground tabular-nums">
        {t(($) => $.quality.measure.coverage, { covered: view.sample, total: runs })}
      </div>
    ) : null;

  return (
    <Card className="gap-3" data-testid={`quality-card-${dimension}`}>
      <CardHeader className="gap-1">
        <CardTitle className="text-body font-medium">{label}</CardTitle>
        <CardDescription className="text-caption">{hint}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-1.5">
        {view.state === "ok" ? (
          <>
            <div className="text-title font-semibold tabular-nums">
              {formatMeasureValue(view.unit, view.value, locale, view.scoreMax)}
            </div>
            {/* Three cases, and the third renders nothing. A ratio prints its
                two sides; a value counted over a named run set prints that
                count; a value counted over nothing — D1's static token estimate
                and its median of medians — prints neither, because "over 0
                runs" under a real number reads as a sample size rather than as
                the absence of one. */}
            {view.numerator !== null && view.denominator !== null ? (
              <div className="text-caption text-muted-foreground">
                {t(($) => $.quality.measure.ratio, {
                  numerator: view.numerator,
                  denominator: view.denominator,
                })}
              </div>
            ) : coverage ? (
              coverage
            ) : view.sample > 0 ? (
              <div className="text-caption text-muted-foreground">
                {t(($) => $.quality.measure.ofRuns, { count: view.sample })}
              </div>
            ) : null}
            {view.excluded > 0 ? (
              <div className="text-caption text-muted-foreground">
                {t(($) => $.quality.measure.excluded, { count: view.excluded })}
              </div>
            ) : null}
          </>
        ) : null}

        {view.state === "no_data" ? (
          <>
            <div className="text-title font-semibold text-muted-foreground">
              {t(($) => $.quality.measure.noData)}
            </div>
            <div className="text-caption text-muted-foreground">{reasonText(t, view.reason)}</div>
            {coverage}
          </>
        ) : null}

        {/* Sample and floor are shown as two counts, never as the ratio between
            them — the whole point of the state is that no rate is warranted. */}
        {view.state === "insufficient_sample" ? (
          <>
            <Badge variant="secondary" className="w-fit">
              {t(($) => $.quality.measure.insufficient)}
            </Badge>
            <div className="text-caption text-muted-foreground tabular-nums">
              {t(($) => $.quality.measure.insufficientDetail, {
                sample: view.sample,
                threshold: view.threshold,
              })}
            </div>
            <div className="text-caption text-muted-foreground">{reasonText(t, view.reason)}</div>
            {coverage}
          </>
        ) : null}

        {/* The trail behind the median: rule, weight and the run it came from,
            collapsed until asked. An empty or absent list renders nothing — it
            is not proof of a clean record, so no "no deductions" line exists. */}
        {deductions && deductions.length > 0 ? (
          <Collapsible className="mt-1">
            <CollapsibleTrigger
              data-testid="quality-deductions-toggle"
              className="w-fit text-caption text-muted-foreground underline-offset-2 hover:underline"
            >
              {t(($) => $.quality.measure.deductions, { count: deductions.length })}
            </CollapsibleTrigger>
            <CollapsibleContent>
              <ul className="mt-2 flex max-h-48 flex-col gap-1 overflow-y-auto">
                {deductions.map((d, i) => (
                  <li key={`${d.task_id}-${d.seq}-${i}`} className="flex items-center gap-2">
                    <Badge variant="outline" className="font-mono">
                      {d.rule}
                    </Badge>
                    <span className="tabular-nums">-{d.points}</span>
                    <span className="font-mono text-caption text-muted-foreground">
                      {d.task_id.slice(0, 8)}·{d.seq}
                    </span>
                  </li>
                ))}
              </ul>
            </CollapsibleContent>
          </Collapsible>
        ) : null}
      </CardContent>
    </Card>
  );
}

/**
 * Translates a server reason key, falling back to the "unknown" phrase for a
 * key this build has no wording for. A key a newer backend adds must not print
 * raw.
 */
function reasonText(t: ReturnType<typeof useT<"self-evolution">>["t"], reason: string): string {
  const known = [
    "no_finished_runs",
    "not_instrumented",
    "no_covered_runs",
    "no_version_content",
    "no_reviewed_issues",
    "below_sample_floor",
    "unknown",
  ] as const;
  const key = (known as readonly string[]).includes(reason)
    ? (reason as (typeof known)[number])
    : "unknown";
  return t(($) => $.quality.measure.reason[key]);
}
