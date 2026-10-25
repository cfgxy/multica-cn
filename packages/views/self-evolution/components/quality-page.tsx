"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { selfEvolutionModelConfigOptions } from "@multica/core/self-evolution";
import { promptQualityDashboardOptions } from "@multica/core/self-evolution";
import { useLocale, useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { DataSourceStatusCard } from "./data-source-status-card";
import { QualityTab } from "./quality-tab";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { VersionsTab } from "./versions-tab";
import { QuizTab } from "./quiz-tab";
import { useNavigation } from "../../navigation";

/**
 * The quality page (RUYI-551 §2.5, fig 7): the existing measure surface,
 * fronted by a four-state status card per data source — scoring model and
 * Langfuse — and a banner whenever data is degraded. The banner replaces the
 * old tooltip footnote: degraded data must be visible without a hover and
 * lead somewhere actionable (walk #8).
 *
 * Source semantics follow §2.5's boundary (v1.1): only the module-config
 * source ever enters the error state, because only it is validated; a
 * deploy-default or system-level source is three-state (unconfigured /
 * available / switched off) and never masquerades a failed run as a config
 * error — run failures stay on the run records and the degraded banner.
 */
export function QualityPage() {
  const wsId = useWorkspaceId();
  const navigation = useNavigation();
  const { t } = useT("self-evolution");
  const tab = qualityTab(navigation.searchParams.get("tab"));
  const [visitedTabs, setVisitedTabs] = useState(() => new Set([tab]));

  useEffect(() => {
    setVisitedTabs((current) => current.has(tab) ? current : new Set(current).add(tab));
  }, [tab]);

  const selectTab = (value: string) => {
    if (value !== "quality" && value !== "versions" && value !== "quiz") return;
    setVisitedTabs((current) => current.has(value) ? current : new Set(current).add(value));
    const params = new URLSearchParams(navigation.searchParams);
    params.set("tab", value);
    navigation.push(`${navigation.pathname}?${params.toString()}${navigation.hash}`);
  };

  return (
    <SelfEvolutionShell>
      <Tabs value={tab} onValueChange={(value) => { if (typeof value === "string") selectTab(value); }} className="min-w-0">
        <TabsList aria-label={t(($) => $.nav.qualityTabs)} className="max-w-full flex-wrap justify-start">
          <TabsTrigger value="quality">{t(($) => $.nav.quality)}</TabsTrigger>
          <TabsTrigger value="versions">{t(($) => $.nav.versions)}</TabsTrigger>
          <TabsTrigger value="quiz">{t(($) => $.nav.quiz)}</TabsTrigger>
        </TabsList>
        <TabsContent value="quality" keepMounted className="min-w-0">
          {visitedTabs.has("quality") ? <QualityPageBody wsId={wsId} /> : null}
        </TabsContent>
        <TabsContent value="versions" keepMounted className="min-w-0">
          {visitedTabs.has("versions") ? <VersionsTab wsId={wsId} /> : null}
        </TabsContent>
        <TabsContent value="quiz" keepMounted className="min-w-0">
          {visitedTabs.has("quiz") ? <QuizTab wsId={wsId} /> : null}
        </TabsContent>
      </Tabs>
    </SelfEvolutionShell>
  );
}

function qualityTab(value: string | null): "quality" | "versions" | "quiz" {
  return value === "versions" || value === "quiz" ? value : "quality";
}

export function QualityPageBody({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const config = useQuery(selfEvolutionModelConfigOptions(wsId));
  // Langfuse is instance-level, so any resolvable scope's dashboard carries
  // the same data_sources block; the workspace scope is the cheapest stable
  // handle that is always queryable.
  const dashboard = useQuery({
    ...promptQualityDashboardOptions(wsId, "workspace", wsId, 30),
    enabled: wsId !== "",
    staleTime: 60_000,
  });

  const scoring = config.data?.resolved;
  const override = config.data?.override;
  const langfuse = dashboard.data?.data_sources.items.find((s) => s.kind === "langfuse");

  return (
    <div className="flex min-w-0 flex-col gap-4 overflow-y-auto">
      <section aria-label={t(($) => $.qualityCards.title)} className="grid gap-3 lg:grid-cols-2">
        <DataSourceStatusCard
          title={t(($) => $.qualityCards.scoringModel)}
          testId="quality-status-scoring"
          status={scoring?.status ?? "unconfigured"}
          effectiveSource={
            scoring?.source === "module_config"
              ? "module_config"
              : scoring?.source === "deploy_default"
                ? "deploy_default"
                : undefined
          }
          reason={scoringReason(t, locale, scoring?.status, scoring?.source, override?.last_validated_at, override?.last_validation_error)}
          impact={scoringImpact(t, scoring?.status)}
          actions={scoringActions(t, paths, scoring?.status, scoring?.source)}
        />
        <DataSourceStatusCard
          title={t(($) => $.qualityCards.langfuse)}
          testId="quality-status-langfuse"
          status={langfuseStatus(langfuse?.available)}
          effectiveSource={langfuse?.available ? "system" : undefined}
          reason={
            langfuse?.available
              ? t(($) => $.qualityCards.reasons.langfuseOk)
              : t(($) => $.qualityCards.reasons.langfuseMissing)
          }
          impact={
            langfuse?.available
              ? t(($) => $.qualityCards.impacts.langfuseOk)
              : t(($) => $.qualityCards.impacts.langfuseMissing)
          }
          actions={langfuse?.available ? undefined : (
            <Button size="sm" variant="outline" disabled>
              {t(($) => $.qualityCards.actions.deployNotes)}
            </Button>
          )}
        />
      </section>

      {dashboard.data?.data_sources.degraded ? (
        <div
          className="flex flex-wrap items-center gap-3 rounded-md border border-blue-500/30 bg-blue-500/5 px-4 py-2.5 text-body"
          data-testid="quality-degraded-banner"
        >
          <span>{t(($) => $.qualityCards.banner)}</span>
          <a href="#quality-status-scoring" className={buttonVariants({ variant: "outline", size: "sm" })}>
            {t(($) => $.qualityCards.viewSources)}
          </a>
        </div>
      ) : null}

      <QualityTab wsId={wsId} />
    </div>
  );
}

function langfuseStatus(available: boolean | undefined) {
  if (available === undefined) return "unconfigured" as const;
  return available ? ("ok" as const) : ("unconfigured" as const);
}

function scoringReason(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  locale: string,
  status: string | undefined,
  source: string | undefined,
  lastValidatedAt: string | undefined,
  lastError: string | undefined,
) {
  switch (status) {
    case "ok":
      return source === "module_config"
        ? lastValidatedAt
          ? t(($) => $.qualityCards.reasons.okValidated, {
              date: new Date(lastValidatedAt).toLocaleString(locale),
            })
          : t(($) => $.qualityCards.reasons.okModule)
        : t(($) => $.qualityCards.reasons.okDeploy);
    case "error":
      return lastError
        ? t(($) => $.qualityCards.reasons.errorDetail, { error: lastError })
        : t(($) => $.qualityCards.reasons.error);
    case "disabled":
      return t(($) => $.qualityCards.reasons.disabled);
    default:
      return t(($) => $.qualityCards.reasons.unconfigured);
  }
}

function scoringImpact(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  status: string | undefined,
) {
  switch (status) {
    case "ok":
      return t(($) => $.qualityCards.impacts.ok);
    case "error":
      return t(($) => $.qualityCards.impacts.error);
    case "disabled":
      return t(($) => $.qualityCards.impacts.disabled);
    default:
      return t(($) => $.qualityCards.impacts.unconfigured);
  }
}

function scoringActions(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  paths: ReturnType<typeof useWorkspacePaths>,
  status: string | undefined,
  source: string | undefined,
) {
  switch (status) {
    case "ok":
      return (
        <AppLink
          href={paths.selfEvolutionConfig()}
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          {t(($) => $.qualityCards.actions.viewConfig)}
        </AppLink>
      );
    case "error":
      return (
        <AppLink
          href={paths.selfEvolutionConfig()}
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          {t(($) => $.qualityCards.actions.viewError)}
        </AppLink>
      );
    case "disabled":
      return (
        <AppLink
          href={paths.selfEvolutionConfig()}
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          {t(($) => $.qualityCards.actions.enable)}
        </AppLink>
      );
    default:
      return (
        <AppLink
          href={source === "deploy_default" ? paths.selfEvolutionConfig() : paths.selfEvolutionConfig()}
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          {t(($) => $.qualityCards.actions.configure)}
        </AppLink>
      );
  }
}
