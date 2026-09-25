"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import type { MeasureView } from "@multica/core/self-evolution";
import type { PromptQualityDimension } from "@multica/core/types";
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
}: {
  dimension: PromptQualityDimension;
  view: MeasureView;
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();

  const label = t(($) => $.quality.dimensions[dimension].label);
  const hint = t(($) => $.quality.dimensions[dimension].hint);

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
              {formatMeasureValue(dimension, view.value, locale)}
            </div>
            <div className="text-caption text-muted-foreground">
              {view.numerator !== null && view.denominator !== null
                ? t(($) => $.quality.measure.ratio, {
                    numerator: view.numerator,
                    denominator: view.denominator,
                  })
                : t(($) => $.quality.measure.ofRuns, { count: view.sample })}
            </div>
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
          </>
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
