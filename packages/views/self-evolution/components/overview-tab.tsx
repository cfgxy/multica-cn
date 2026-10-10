"use client";

import { useQuery } from "@tanstack/react-query";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  PROMPT_QUALITY_DIMENSIONS,
  selfEvolutionOverviewOptions,
  toMeasureView,
} from "@multica/core/self-evolution";
import type { MeasureView } from "@multica/core/self-evolution";
import { formatMeasureValue } from "./quality-format";
import { AppLink } from "../../navigation";
import { useLocale, useT } from "../../i18n";
import { useWorkspacePaths } from "@multica/core/paths";

/**
 * The self-evolution overview tab (RUYI-284).
 *
 * One read-only lens across the data planes the other tabs own. Every
 * number here is served by the workspace aggregate endpoint, which folds the
 * same rows through the same code paths as the tabs — so a section can only
 * disagree with its tab through a bug, not by construction. The overview is a
 * lens, not a surface: there is deliberately no write control anywhere in this
 * tree, and the component test holds that line.
 *
 * Proposal metrics keep their section slot but stay deliberately unwired
 * (Leader boundary on RUYI-284, 2026-09-30): the pool's model is being
 * replaced by RUYI-305, and reading the current table would be rework.
 *
 * Three kinds of emptiness stay distinguishable, per the acceptance spec: a
 * plane with nothing in it says so in its own section; a plane whose sample is
 * below the declared floor shows the server's marker (never a substituted
 * number); and a failed fetch is an explicit error state with a retry, never a
 * silent zero.
 */
export function OverviewTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const overview = useQuery(selfEvolutionOverviewOptions(wsId));

  if (overview.isPending) {
    return (
      <div className="grid gap-4 md:grid-cols-2" aria-busy="true" aria-label={t(($) => $.overview.loading)}>
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-28 w-full" />
        ))}
      </div>
    );
  }

  if (overview.isError) {
    return (
      <div
        role="alert"
        className="flex flex-col gap-2 rounded-md border border-destructive/40 p-4"
        data-testid="overview-error"
      >
        <p className="text-body text-destructive">{t(($) => $.overview.error)}</p>
        <Button
          size="sm"
          variant="outline"
          className="w-fit"
          onClick={() => void overview.refetch()}
        >
          {t(($) => $.overview.retry)}
        </Button>
      </div>
    );
  }

  const data = overview.data;

  const tierLabel = (scope: string) => {
    switch (scope) {
      case "workspace": return t(($) => $.quality.scopePicker.workspace);
      case "project": return t(($) => $.quality.scopePicker.project);
      case "squad": return t(($) => $.quality.scopePicker.squad);
      case "agent": return t(($) => $.quality.scopePicker.agent);
      default: return scope;
    }
  };
  const verdictLabel = (verdict: string) => {
    switch (verdict) {
      case "steady": return t(($) => $.quiz.reading.verdict.steady);
      case "improved": return t(($) => $.quiz.reading.verdict.improved);
      case "regressed": return t(($) => $.quiz.reading.verdict.regressed);
      default: return t(($) => $.quiz.reading.verdict.insufficient);
    }
  };
  const scanLabel = (result: string) => {
    switch (result) {
      case "noop": return t(($) => $.knowledge.scanResult.noop);
      case "changed": return t(($) => $.knowledge.scanResult.changed);
      case "failed": return t(($) => $.knowledge.scanResult.failed);
      default: return result;
    }
  };
  const measureChip = (key: (typeof PROMPT_QUALITY_DIMENSIONS)[number]) => {
    const view: MeasureView = toMeasureView(data.quality.measures[key]);
    return (
      <li
        key={key}
        className="flex items-center justify-between gap-2 rounded border border-border px-2 py-1 text-caption"
        data-testid={`overview-measure-${key}`}
      >
        <span className="min-w-0 truncate text-muted-foreground">
          {t(($) => $.quality.dimensions[key].label)}
        </span>
        {view.state === "ok" ? (
          <span className="font-medium">
            {formatMeasureValue(view.unit, view.value, locale, view.scoreMax)}
          </span>
        ) : view.state === "insufficient_sample" ? (
          <Badge variant="outline">{t(($) => $.quality.measure.insufficient)}</Badge>
        ) : (
          <Badge variant="outline">{t(($) => $.quality.measure.noData)}</Badge>
        )}
      </li>
    );
  };

  return (
    <div className="grid gap-4 md:grid-cols-2">
      <section
        className="space-y-2 rounded-md border border-border p-4"
        aria-label={t(($) => $.overview.versions.title)}
        data-testid="overview-versions"
      >
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-title font-medium">{t(($) => $.overview.versions.title)}</h3>
          <AppLink href={paths.selfEvolutionVersions()} className="shrink-0 text-caption text-primary" data-testid="overview-versions-link">
            {t(($) => $.overview.viewAll)}
          </AppLink>
        </div>
        {data.versions.length === 0 ? (
          <p className="text-body text-muted-foreground">{t(($) => $.overview.versions.empty)}</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {data.versions.map((tier) => (
              <li
                key={tier.scope}
                className="flex flex-wrap items-center gap-2 text-body"
                data-testid={`overview-tier-${tier.scope}`}
              >
                <Badge variant="outline">{tierLabel(tier.scope)}</Badge>
                {tier.current_version !== undefined ? (
                  <span className="font-medium">
                    {t(($) => $.overview.versions.currentVersion, { version: tier.current_version })}
                  </span>
                ) : (
                  <span className="text-muted-foreground">
                    {t(($) => $.overview.versions.currentUnset, { count: tier.subject_count })}
                  </span>
                )}
                <span className="text-caption text-muted-foreground">
                  {t(($) => $.overview.versions.count, {
                    count: tier.version_count,
                    subjects: tier.subject_count,
                  })}
                </span>
                {tier.last_change_at ? (
                  <span className="ml-auto text-caption text-muted-foreground">
                    {tier.last_actor
                      ? t(($) => $.overview.versions.lastChange, {
                          date: new Date(tier.last_change_at).toLocaleString(locale),
                          actor: tier.last_actor,
                        })
                      : t(($) => $.overview.versions.lastChangeNoActor, {
                          date: new Date(tier.last_change_at).toLocaleString(locale),
                        })}
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section
        className="space-y-2 rounded-md border border-border p-4"
        aria-label={t(($) => $.overview.quality.title, { days: data.quality.days })}
        data-testid="overview-quality"
      >
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-title font-medium">
            {t(($) => $.overview.quality.title, { days: data.quality.days })}
          </h3>
          <AppLink href={paths.selfEvolutionQuality()} className="shrink-0 text-caption text-primary" data-testid="overview-quality-link">
            {t(($) => $.overview.viewAll)}
          </AppLink>
        </div>
        {data.quality.subjects_measured === 0 ? (
          <p className="text-body text-muted-foreground">{t(($) => $.overview.quality.empty)}</p>
        ) : (
          <>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.overview.quality.runs, { count: data.quality.runs })} ·{" "}
              {t(($) => $.overview.quality.subjects, { count: data.quality.subjects_measured })}
            </p>
            <ul className="grid grid-cols-2 gap-1.5 xl:grid-cols-4">
              {PROMPT_QUALITY_DIMENSIONS.map((key) => measureChip(key))}
            </ul>
            {data.quality.excluded_failed_runs > 0 ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.overview.quality.excluded, { count: data.quality.excluded_failed_runs })}
              </p>
            ) : null}
          </>
        )}
      </section>

      <section
        className="space-y-2 rounded-md border border-border p-4"
        aria-label={t(($) => $.overview.quiz.title)}
        data-testid="overview-quiz"
      >
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-title font-medium">{t(($) => $.overview.quiz.title)}</h3>
          <AppLink href={paths.selfEvolutionQuiz()} className="shrink-0 text-caption text-primary" data-testid="overview-quiz-link">
            {t(($) => $.overview.viewAll)}
          </AppLink>
        </div>
        {!data.quiz.scope_id ? (
          <p className="text-body text-muted-foreground">{t(($) => $.overview.quiz.empty)}</p>
        ) : (
          <div className="flex flex-col gap-1.5">
            <p className="flex flex-wrap items-center gap-2 text-body">
              <span className="min-w-0 truncate font-medium">
                {data.quiz.scope_name || data.quiz.scope_id}
              </span>
              {data.quiz.current_version !== undefined && data.quiz.baseline_version !== undefined ? (
                <span className="text-caption text-muted-foreground">
                  {t(($) => $.overview.quiz.versions, {
                    baseline: data.quiz.baseline_version,
                    current: data.quiz.current_version,
                  })}
                </span>
              ) : data.quiz.current_version !== undefined ? (
                <span className="text-caption text-muted-foreground">
                  {t(($) => $.overview.quiz.singleVersion, { current: data.quiz.current_version })}
                </span>
              ) : null}
              {data.quiz.verdict === "insufficient" && data.quiz.measured ? (
                <Badge variant="outline">
                  {t(($) => $.overview.quiz.insufficientSample, {
                    baseline: data.quiz.required_baseline,
                    current: data.quiz.required_sample,
                  })}
                </Badge>
              ) : (
                <Badge
                  variant={
                    data.quiz.verdict === "steady" || data.quiz.verdict === "improved" || data.quiz.verdict === "regressed"
                      ? "secondary"
                      : "outline"
                  }
                >
                  {verdictLabel(data.quiz.verdict)}
                </Badge>
              )}
            </p>
            {data.quiz.last_measured_at ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.overview.quiz.lastMeasured, {
                  date: new Date(data.quiz.last_measured_at).toLocaleString(locale),
                })}
              </p>
            ) : null}
          </div>
        )}
      </section>

      <section
        className="space-y-2 rounded-md border border-border p-4"
        aria-label={t(($) => $.overview.proposals.title)}
        data-testid="overview-proposals"
      >
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-title font-medium">{t(($) => $.overview.proposals.title)}</h3>
          <AppLink href={paths.selfEvolutionProposals()} className="shrink-0 text-caption text-primary" data-testid="overview-proposals-link">
            {t(($) => $.overview.viewAll)}
          </AppLink>
        </div>
        {/* Deliberately unwired: the pool's model is being replaced by
            RUYI-305, so reading the current behavior-prophecy table here
            would be rework the moment it lands. The section keeps its slot
            with an explicit pending note instead of a lying zero. */}
        <p className="text-body text-muted-foreground">
          {t(($) => $.overview.proposals.pendingModel)}
        </p>
      </section>

      <section
        className="space-y-2 rounded-md border border-border p-4"
        aria-label={t(($) => $.overview.knowledge.title)}
        data-testid="overview-knowledge"
      >
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-title font-medium">{t(($) => $.overview.knowledge.title)}</h3>
          <AppLink href={paths.selfEvolutionKnowledge()} className="shrink-0 text-caption text-primary" data-testid="overview-knowledge-link">
            {t(($) => $.overview.viewAll)}
          </AppLink>
        </div>
        {data.knowledge.dirs === 0 && data.knowledge.entries === 0 ? (
          <p className="text-body text-muted-foreground">{t(($) => $.overview.knowledge.empty)}</p>
        ) : (
          <div className="flex flex-col gap-1">
            <p className="text-body">
              {t(($) => $.overview.knowledge.dirs, { count: data.knowledge.dirs })} ·{" "}
              {t(($) => $.overview.knowledge.entries, { count: data.knowledge.entries })}
            </p>
            {data.knowledge.last_scan ? (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.overview.knowledge.lastScan, {
                  result: scanLabel(data.knowledge.last_scan.result),
                  date: new Date(data.knowledge.last_scan.started_at).toLocaleString(locale),
                })}
              </p>
            ) : (
              <p className="text-caption text-muted-foreground">
                {t(($) => $.knowledge.neverScanned)}
              </p>
            )}
          </div>
        )}
      </section>

      <section
        className="space-y-2 rounded-md border border-border p-4"
        aria-label={t(($) => $.overview.skills.title)}
        data-testid="overview-skills"
      >
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-title font-medium">{t(($) => $.overview.skills.title)}</h3>
          <AppLink href={paths.selfEvolutionSkills()} className="shrink-0 text-caption text-primary" data-testid="overview-skills-link">
            {t(($) => $.overview.viewAll)}
          </AppLink>
        </div>
        {data.skills.count === 0 ? (
          <p className="text-body text-muted-foreground">{t(($) => $.overview.skills.empty)}</p>
        ) : (
          <div className="flex flex-col gap-1">
            <p className="text-body">{t(($) => $.overview.skills.count, { count: data.skills.count })}</p>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.overview.skills.invocations, { count: data.skills.invocations })}
            </p>
          </div>
        )}
      </section>
    </div>
  );
}
