"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@multica/ui/components/ui/collapsible";
import { Progress, ProgressTrack, ProgressIndicator } from "@multica/ui/components/ui/progress";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import type { PromptPerplexityItem, PromptQualityPerplexity } from "@multica/core/types";
import { useT, useLocale } from "../../i18n";

/**
 * Sub-dimension weights, as the scorer applies them
 * (`server/pkg/promptperplexity/score.go`). They are duplicated here rather
 * than sent per response because they are part of the scoring rubric, not of a
 * particular score: a weight that changed would change what every stored score
 * means, so it ships with the build that knows how to read those scores.
 */
const SUB_DIMENSION_WEIGHTS: Record<string, number> = {
  tier_placement: 0.15,
  cross_tier_conflict: 0.2,
  literal_actionability: 0.15,
  internal_consistency: 0.15,
  precedence_clarity: 0.15,
  scope_boundary: 0.1,
  role_rule_completeness: 0.1,
};

const KNOWN_SUB_DIMENSIONS = [
  "tier_placement",
  "cross_tier_conflict",
  "literal_actionability",
  "internal_consistency",
  "precedence_clarity",
  "scope_boundary",
  "role_rule_completeness",
] as const;

/**
 * The D3 drill-down (RUYI-184 UIUX §3.4).
 *
 * One score belongs to one runtime profile; the caller picks which profile is
 * open and this sheet never merges two. Evidence entries carry locations only
 * — tier and section names, never prompt text — which is what the source-B
 * rule allows to leave the server.
 */
export function QualityPerplexitySheet({
  score,
  open,
  onOpenChange,
}: {
  score: PromptQualityPerplexity | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full gap-0 overflow-y-auto sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>{t(($) => $.quality.perplexity.sheet.title)}</SheetTitle>
          <SheetDescription>{t(($) => $.quality.perplexity.sheet.description)}</SheetDescription>
        </SheetHeader>

        {score === null ? null : (
          <div className="flex flex-col gap-4 px-4 pb-6">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline">
                {t(($) => $.quality.timeline.version, { version: score.version })}
              </Badge>
              <Badge variant="secondary">{profileLabel(t, score.runtime_profile)}</Badge>
              <Badge variant="outline">{bandLabel(t, score.band)}</Badge>
              {score.percent_low !== null && score.percent_high !== null ? (
                <span className="text-caption text-muted-foreground tabular-nums">
                  {t(($) => $.quality.perplexity.range, {
                    low: score.percent_low,
                    high: score.percent_high,
                  })}
                </span>
              ) : null}
            </div>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.quality.perplexity.scoredAt, {
                date: formatDate(score.scored_at, locale),
                model: score.model,
              })}
            </p>
            {/* The declared fluctuation range. A band and an interval produced
                by a model are only readable next to how much they move when
                the same document is scored again; the number comes from
                `server/pkg/promptperplexity/repeatability_integration_test.go`. */}
            <p className="text-caption text-muted-foreground">
              {t(($) => $.quality.perplexity.reproducibility)}
            </p>

            <div className="flex flex-col gap-3">
              {score.evidence.map((item) => (
                <SubDimensionRow key={item.key} item={item} />
              ))}
            </div>
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}

function SubDimensionRow({ item }: { item: PromptPerplexityItem }) {
  const { t } = useT("self-evolution");
  const entries = item.evidence ?? [];

  return (
    <div className="flex flex-col gap-2 rounded-md border border-border p-3">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-body font-medium">{subDimensionLabel(t, item.key)}</span>
        <span className="text-caption text-muted-foreground tabular-nums">
          {t(($) => $.quality.perplexity.sheet.weight, {
            percent: Math.round((SUB_DIMENSION_WEIGHTS[item.key] ?? 0) * 100),
          })}
        </span>
      </div>

      <Progress value={Math.round(clamp01(item.score) * 100)}>
        <ProgressTrack>
          <ProgressIndicator />
        </ProgressTrack>
      </Progress>

      <p className="line-clamp-2 text-caption text-muted-foreground">{item.justification}</p>

      <Collapsible>
        <CollapsibleTrigger className="flex w-fit items-center gap-1 text-caption text-muted-foreground transition-colors hover:text-foreground">
          {`${t(($) => $.quality.perplexity.sheet.evidence)} · ${t(
            ($) => $.quality.perplexity.sheet.evidenceCount,
            { count: entries.length },
          )}`}
        </CollapsibleTrigger>
        <CollapsibleContent className="flex flex-col gap-1.5 pt-2">
          {/* An empty evidence list is stated, not left blank: a sub-dimension
              scored without citable session logs must say so (UIUX §3.4). */}
          {entries.length === 0 ? (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.quality.perplexity.sheet.noEvidence)}
            </p>
          ) : (
            entries.map((entry, index) => (
              <div
                key={`${entry.tier}-${entry.section}-${index}`}
                className="flex flex-col gap-0.5 text-caption"
              >
                <span className="font-mono">{`${entry.tier} · ${entry.section}`}</span>
                {entry.conflicts_with !== undefined && entry.conflicts_with !== "" ? (
                  <span className="text-muted-foreground">
                    {t(($) => $.quality.perplexity.sheet.conflictsWith, {
                      target: entry.conflicts_with,
                    })}
                  </span>
                ) : null}
                {entry.note !== undefined && entry.note !== "" ? (
                  <span className="text-muted-foreground">{entry.note}</span>
                ) : null}
              </div>
            ))
          )}
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}

function clamp01(v: number): number {
  if (!Number.isFinite(v)) return 0;
  return Math.min(1, Math.max(0, v));
}

export function subDimensionLabel(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  key: string,
): string {
  const known = (KNOWN_SUB_DIMENSIONS as readonly string[]).includes(key);
  return known
    ? t(($) => $.quality.perplexity.sheet.subDimensions[key as (typeof KNOWN_SUB_DIMENSIONS)[number]])
    : t(($) => $.quality.perplexity.sheet.subDimensions.unknown, { key });
}

export function profileLabel(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  profile: string,
): string {
  if (profile === "member") return t(($) => $.quality.perplexity.profile.member);
  if (profile === "leader_task") return t(($) => $.quality.perplexity.profile.leader_task);
  return t(($) => $.quality.perplexity.profile.unknown, { key: profile });
}

export function bandLabel(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  band: string,
): string {
  if (band === "low") return t(($) => $.quality.perplexity.band.low);
  if (band === "medium") return t(($) => $.quality.perplexity.band.medium);
  if (band === "high") return t(($) => $.quality.perplexity.band.high);
  return t(($) => $.quality.perplexity.band.unknown);
}

function formatDate(value: string, locale: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleDateString(locale, { year: "numeric", month: "short", day: "numeric" });
}
