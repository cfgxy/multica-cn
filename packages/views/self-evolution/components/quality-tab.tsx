"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import {
  degradedSourceKinds,
  groupPerplexityByProfile,
  promptQualityDashboardOptions,
  toDimensionCards,
} from "@multica/core/self-evolution";
import { agentListOptions } from "@multica/core/workspace/queries";
import type { Agent, PromptQualityVersionMeasures } from "@multica/core/types";
import { useT, useLocale } from "../../i18n";
import { QualityCompareDialog } from "./quality-compare-dialog";
import { QualityMeasureCard } from "./quality-measure-card";
import { QualityPerplexitySheet, bandLabel, profileLabel } from "./quality-perplexity-sheet";

const WINDOW_DAYS = [7, 30, 90] as const;

/**
 * The prompt quality dashboard (RUYI-184).
 *
 * The scope picker is agent-only for now because the agent tier is the one D3
 * is keyed on; the endpoint takes all four scopes, so widening this is a
 * picker change rather than a data change.
 */
export function QualityTab({
  wsId,
  initialAgentId = "",
}: {
  wsId: string;
  /** Preselects the subject. The page leaves it empty; tests supply one. */
  initialAgentId?: string;
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();

  const [agentId, setAgentId] = useState(initialAgentId);
  const [days, setDays] = useState<number>(30);
  const [profile, setProfile] = useState("member");
  const [openScoreVersion, setOpenScoreVersion] = useState<number | null>(null);
  const [selectedVersions, setSelectedVersions] = useState<number[]>([]);

  const agents = useQuery(agentListOptions(wsId));
  const dashboard = useQuery(promptQualityDashboardOptions(wsId, "agent", agentId, days));

  const agentItems = useMemo(
    () => (agents.data ?? []).map((a: Agent) => ({ value: a.id, label: a.name })),
    [agents.data],
  );

  const byProfile = useMemo(
    () => groupPerplexityByProfile(dashboard.data?.perplexity ?? []),
    [dashboard.data],
  );
  const profileKeys = useMemo(() => [...byProfile.keys()], [byProfile]);
  const activeProfile = profileKeys.includes(profile) ? profile : (profileKeys[0] ?? "");
  const profileScores = byProfile.get(activeProfile) ?? [];
  const openScore =
    openScoreVersion === null
      ? null
      : (profileScores.find((s) => s.version === openScoreVersion) ?? null);

  const versions = dashboard.data?.versions ?? [];
  const compareLeft = findVersion(versions, selectedVersions[0]);
  const compareRight = findVersion(versions, selectedVersions[1]);

  const degraded = dashboard.data ? degradedSourceKinds(dashboard.data.data_sources) : [];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1.5">
          <span className="text-caption text-muted-foreground">
            {t(($) => $.quality.scopePicker.subjectLabel)}
          </span>
          <Select
            items={agentItems}
            value={agentId}
            onValueChange={(next) => {
              if (typeof next === "string") {
                setAgentId(next);
                setSelectedVersions([]);
                setOpenScoreVersion(null);
              }
            }}
          >
            <SelectTrigger size="sm" className="w-56">
              <SelectValue>
                {agentItems.find((i) => i.value === agentId)?.label ??
                  t(($) => $.quality.scopePicker.subjectPlaceholder)}
              </SelectValue>
            </SelectTrigger>
            <SelectContent align="start" className="max-h-72">
              {agentItems.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="flex flex-col gap-1.5">
          <span className="text-caption text-muted-foreground">
            {t(($) => $.quality.range.label)}
          </span>
          <div className="flex gap-1">
            {WINDOW_DAYS.map((n) => (
              <Button
                key={n}
                size="sm"
                variant={days === n ? "secondary" : "ghost"}
                onClick={() => setDays(n)}
              >
                {t(($) => $.quality.range.days, { count: n })}
              </Button>
            ))}
          </div>
        </div>
      </div>

      {agentId === "" ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.quality.noSubject.title)}</EmptyTitle>
            <EmptyDescription>{t(($) => $.quality.noSubject.description)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : dashboard.isPending ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 7 }, (_, i) => (
            <Skeleton key={i} className="h-32 w-full rounded-lg" />
          ))}
        </div>
      ) : dashboard.isError ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.quality.error.title)}</EmptyTitle>
          </EmptyHeader>
          <Button size="sm" variant="outline" onClick={() => void dashboard.refetch()}>
            {t(($) => $.quality.error.retry)}
          </Button>
        </Empty>
      ) : dashboard.data === undefined ? null : (
        <>
          <p className="text-caption text-muted-foreground">
            {t(($) => $.quality.since, { date: dashboard.data.since })}
          </p>

          <section className="flex flex-col gap-3">
            <div className="flex items-baseline gap-2">
              <h3 className="text-body font-medium">{t(($) => $.quality.window.title)}</h3>
              <span className="text-caption text-muted-foreground">
                {t(($) => $.quality.window.runs, { count: dashboard.data.window.runs })}
              </span>
            </div>
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {toDimensionCards(dashboard.data.window.measures).map((card) => (
                <QualityMeasureCard key={card.key} dimension={card.key} view={card.view} />
              ))}
            </div>
          </section>

          <section className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-body font-medium">{t(($) => $.quality.timeline.title)}</h3>
              <span className="text-caption text-muted-foreground">
                {t(($) => $.quality.timeline.compareHint)}
              </span>
              {selectedVersions.length > 0 ? (
                <Button size="sm" variant="ghost" onClick={() => setSelectedVersions([])}>
                  {t(($) => $.quality.timeline.clear)}
                </Button>
              ) : null}
            </div>
            {versions.length === 0 ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.quality.timeline.empty)}
              </p>
            ) : (
              <div className="flex flex-wrap gap-2">
                {versions.map((v) => (
                  <button
                    key={v.version}
                    type="button"
                    onClick={() => setSelectedVersions((prev) => toggleVersion(prev, v.version))}
                    className={cn(
                      "flex flex-col items-start gap-0.5 rounded-md border px-3 py-2 text-left transition-colors",
                      selectedVersions.includes(v.version)
                        ? "border-primary bg-muted font-medium"
                        : "border-border hover:bg-muted/50",
                    )}
                  >
                    <span className="text-caption">
                      {t(($) => $.quality.timeline.version, { version: v.version })}
                    </span>
                    <span className="text-caption text-muted-foreground tabular-nums">
                      {t(($) => $.quality.timeline.span, {
                        first: v.first_day ?? "",
                        last: v.last_day ?? "",
                      })}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </section>

          <section className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-body font-medium">{t(($) => $.quality.perplexity.title)}</h3>
              <span className="text-caption text-muted-foreground">
                {t(($) => $.quality.perplexity.description)}
              </span>
            </div>

            {profileKeys.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>{t(($) => $.quality.perplexity.empty.title)}</EmptyTitle>
                  <EmptyDescription>
                    {t(($) => $.quality.perplexity.empty.description)}
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <>
                {/* Each profile is its own document and its own score; the
                    switch moves between them and never combines them (Q17). */}
                <div className="flex flex-wrap items-center gap-1">
                  {profileKeys.map((key) => (
                    <Button
                      key={key}
                      size="sm"
                      variant={key === activeProfile ? "secondary" : "ghost"}
                      onClick={() => setProfile(key)}
                    >
                      {profileLabel(t, key)}
                    </Button>
                  ))}
                  <span className="pl-2 text-caption text-muted-foreground">
                    {t(($) => $.quality.perplexity.profileNote)}
                  </span>
                </div>
                <div className="flex flex-wrap gap-2">
                  {profileScores.map((score) => (
                    <button
                      key={`${score.runtime_profile}-${score.version}`}
                      type="button"
                      onClick={() => setOpenScoreVersion(score.version)}
                      className="flex items-center gap-2 rounded-md border border-border px-3 py-2 transition-colors hover:bg-muted/50"
                    >
                      <Badge variant="outline">
                        {t(($) => $.quality.timeline.version, { version: score.version })}
                      </Badge>
                      <span className="text-caption">{bandLabel(t, score.band)}</span>
                      {score.percent_low !== null && score.percent_high !== null ? (
                        <span className="text-caption text-muted-foreground tabular-nums">
                          {t(($) => $.quality.perplexity.range, {
                            low: score.percent_low,
                            high: score.percent_high,
                          })}
                        </span>
                      ) : null}
                    </button>
                  ))}
                </div>
              </>
            )}
          </section>

          <section className="flex flex-col gap-2">
            <h3 className="text-body font-medium">{t(($) => $.quality.reasons.title)}</h3>
            {Object.keys(dashboard.data.window.failure_reasons).length === 0 ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.quality.reasons.empty)}
              </p>
            ) : (
              <div className="flex flex-wrap gap-2">
                {Object.entries(dashboard.data.window.failure_reasons).map(([reason, count]) => (
                  <Badge key={reason} variant="outline" className="font-mono">
                    {`${reason} · ${new Intl.NumberFormat(locale).format(count)}`}
                  </Badge>
                ))}
              </div>
            )}
            {dashboard.data.window.excluded_failed_runs > 0 ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.quality.reasons.excluded, {
                  count: dashboard.data.window.excluded_failed_runs,
                })}
              </p>
            ) : null}
          </section>

          {/* T3: an optional export being off is a footer line and nothing
              more — no card above is gated on it. */}
          {degraded.length > 0 ? (
            <footer className="flex flex-wrap gap-2 pt-2">
              {degraded.map((kind) => (
                <Tooltip key={kind}>
                  <TooltipTrigger
                    className="text-caption text-muted-foreground"
                    data-testid={`degraded-${kind}`}
                  >
                    {sourceLabel(t, kind)}
                  </TooltipTrigger>
                  <TooltipContent>{t(($) => $.quality.sources.note)}</TooltipContent>
                </Tooltip>
              ))}
            </footer>
          ) : null}

          <QualityPerplexitySheet
            score={openScore}
            open={openScore !== null}
            onOpenChange={(next) => {
              if (!next) setOpenScoreVersion(null);
            }}
          />
          <QualityCompareDialog
            left={compareLeft}
            right={compareRight}
            open={compareLeft !== null && compareRight !== null}
            onOpenChange={(next) => {
              if (!next) setSelectedVersions([]);
            }}
          />
        </>
      )}
    </div>
  );
}

function findVersion(
  versions: PromptQualityVersionMeasures[],
  version: number | undefined,
): PromptQualityVersionMeasures | null {
  if (version === undefined) return null;
  return versions.find((v) => v.version === version) ?? null;
}

/** Two at a time; picking a third drops the older selection. */
function toggleVersion(prev: number[], version: number): number[] {
  if (prev.includes(version)) return prev.filter((v) => v !== version);
  if (prev.length < 2) return [...prev, version];
  return [prev[1] as number, version];
}

function sourceLabel(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  kind: string,
): string {
  if (kind === "langfuse") return t(($) => $.quality.sources.langfuse);
  if (kind === "scoring_model") return t(($) => $.quality.sources.scoring_model);
  return t(($) => $.quality.sources.unknown, { kind });
}
