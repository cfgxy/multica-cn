"use client";

import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, ArrowLeft, Bot, Building2, CheckCircle2, FolderKanban, RefreshCw, Scale, Users } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { CartesianGrid, Line, LineChart, ReferenceLine, XAxis, YAxis } from "recharts";
import type {
  ClauseGovernanceBinding,
  ClauseGovernanceClause,
  ClauseGovernanceDetail,
  ClauseGovernanceQualityDay,
  ClauseGovernanceReconciliation,
  ClauseGovernanceVersion,
} from "@multica/core/types";
import {
  clauseGovernanceDetailOptions,
  clauseGovernanceOverviewOptions,
  clauseGovernanceReconciliationOptions,
} from "@multica/core/self-evolution";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Progress } from "@multica/ui/components/ui/progress";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@multica/ui/components/ui/chart";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { AppLink, useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { diffPromptText } from "../../market/prompt-diff";
import { ProposalTab } from "./proposal-tab";

const WINDOW_OPTIONS = [7, 30, 90, 365] as const;
type ClauseStatusFilter = "all" | "active" | "deprecated" | "revoked";
type SortField = "injections" | "runs" | "injected_tokens" | "title";

function readDays(searchParams: URLSearchParams): number {
  const value = Number(searchParams.get("days"));
  return WINDOW_OPTIONS.includes(value as (typeof WINDOW_OPTIONS)[number]) ? value : 30;
}

function shortId(value: string): string {
  return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function numberLabel(value: number): string {
  return new Intl.NumberFormat().format(value);
}

function statusClass(status: string): string {
  switch (status) {
    case "active":
      return "border-transparent bg-success/10 text-success dark:bg-success/20";
    case "deprecated":
      return "border-transparent bg-warning/10 text-warning dark:bg-warning/20";
    case "revoked":
      return "border-destructive/30 text-destructive";
    default:
      return "";
  }
}

function ScopeBadge({ scope, label }: { scope: string; label: string }) {
  const Icon = scope === "workspace" ? Building2
    : scope === "project" ? FolderKanban
      : scope === "squad" ? Users
        : scope === "agent" ? Bot
          : null;
  return <Badge variant="outline" className="inline-flex items-center gap-1">
    {Icon ? <Icon aria-hidden className="size-3" /> : null}
    {label || scope}
  </Badge>;
}

function enumLabel<Key extends string>(value: string, keys: readonly Key[], label: (key: Key) => string): string {
  const key = keys.find((candidate) => candidate === value);
  return key ? label(key) : value || "—";
}

export function ClauseGovernancePage() {
  const wsId = useWorkspaceId();
  const navigation = useNavigation();
  const { t } = useT("self-evolution");
  const tab = navigation.searchParams.get("tab") === "proposals" ? "proposals" : "clauses";
  const [visitedTabs, setVisitedTabs] = useState(() => new Set([tab]));

  useEffect(() => {
    setVisitedTabs((current) => current.has(tab) ? current : new Set(current).add(tab));
  }, [tab]);

  const selectTab = (value: string) => {
    if (value !== "clauses" && value !== "proposals") return;
    setVisitedTabs((current) => current.has(value) ? current : new Set(current).add(value));
    const params = new URLSearchParams(navigation.searchParams);
    params.set("tab", value);
    navigation.push(`${navigation.pathname}?${params.toString()}${navigation.hash}`);
  };

  return (
    <SelfEvolutionShell>
      <Tabs value={tab} onValueChange={(value) => { if (typeof value === "string") selectTab(value); }} className="min-w-0">
        <TabsList aria-label={t(($) => $.nav.governanceTabs)} className="max-w-full flex-wrap justify-start">
          <TabsTrigger value="clauses">{t(($) => $.nav.clauses)}</TabsTrigger>
          <TabsTrigger value="proposals">{t(($) => $.nav.proposals)}</TabsTrigger>
        </TabsList>
        <TabsContent value="clauses" keepMounted className="min-w-0">
          {visitedTabs.has("clauses") ? <ClauseGovernanceContent /> : null}
        </TabsContent>
        <TabsContent value="proposals" keepMounted className="min-w-0">
          {visitedTabs.has("proposals") ? <ProposalTab wsId={wsId} /> : null}
        </TabsContent>
      </Tabs>
    </SelfEvolutionShell>
  );
}

function ClauseGovernanceContent() {
  const wsId = useWorkspaceId();
  const { t } = useT("self-evolution");
  const navigation = useNavigation();
  const paths = useWorkspacePaths();
  const days = readDays(navigation.searchParams);
  const [statusFilter, setStatusFilter] = useState<ClauseStatusFilter>("all");
  const [sort, setSort] = useState<{ field: SortField; desc: boolean }>({ field: "injections", desc: true });
  const overview = useQuery(clauseGovernanceOverviewOptions(wsId, days));
  const reconciliation = useQuery(clauseGovernanceReconciliationOptions(wsId, days));
  const data = overview.data;
  const reconciliationData = reconciliation.data;

  const clauses = useMemo(() => {
    const rows = (data?.clauses ?? []).filter((clause) => statusFilter === "all" || clause.status === statusFilter);
    return rows.sort((a, b) => {
      const left = sort.field === "title" ? a.title.toLocaleLowerCase() : a[sort.field];
      const right = sort.field === "title" ? b.title.toLocaleLowerCase() : b[sort.field];
      const order = typeof left === "string" && typeof right === "string"
        ? left.localeCompare(right)
        : Number(left) - Number(right);
      return sort.desc ? -order : order;
    });
  }, [data?.clauses, sort, statusFilter]);

  const setDays = (nextDays: number) => {
    const params = new URLSearchParams(navigation.searchParams);
    params.set("days", String(nextDays));
    navigation.replace(`${navigation.pathname}?${params.toString()}${navigation.hash}`);
  };
  const refresh = () => {
    void overview.refetch();
    void reconciliation.refetch();
  };
  const cycleSort = (field: SortField) => setSort((current) => ({
    field,
    desc: current.field === field ? !current.desc : field !== "title",
  }));

  const findings = (reconciliationData?.layer_set_violations.length ?? 0)
    + (reconciliationData?.drift.reduce((count, carrier) => count + carrier.entries.length, 0) ?? 0);
  const disabled = data?.dynamic_injection_disabled === true;
  const healthy = reconciliationData?.healthy === true;

  return (
      <main className="flex min-w-0 flex-col gap-4 px-1 pb-8" data-testid="clause-governance-page">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div className="flex items-start gap-3">
            <Scale aria-hidden className="mt-1 size-6 text-primary" />
            <div>
              <h1 className="text-title font-semibold">{t(($) => $.clauseGovernance.title)}</h1>
              <p className="text-body text-muted-foreground">{t(($) => $.clauseGovernance.description)}</p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <label htmlFor="clause-governance-days" className="text-caption text-muted-foreground">
              {t(($) => $.clauseGovernance.window)}
            </label>
            <select
              id="clause-governance-days"
              className="h-9 rounded-md border border-input bg-background px-3 text-body"
              value={days}
              onChange={(event) => setDays(Number(event.target.value))}
            >
              {WINDOW_OPTIONS.map((option) => (
                <option key={option} value={option}>{t(($) => $.clauseGovernance[`d${option}` as "d7"])}</option>
              ))}
            </select>
            <Button variant="outline" size="sm" onClick={refresh} disabled={overview.isFetching || reconciliation.isFetching}>
              <RefreshCw aria-hidden className={`mr-2 size-4 ${overview.isFetching || reconciliation.isFetching ? "animate-spin" : ""}`} />
              {t(($) => $.clauseGovernance.refresh)}
            </Button>
          </div>
        </header>

        {overview.isLoading || reconciliation.isLoading ? (
          <OverviewSkeleton />
        ) : (
          <>
            {overview.isError ? <QueryError retry={() => void overview.refetch()} /> : null}
            {reconciliation.isError ? <QueryError retry={() => void reconciliation.refetch()} /> : null}
            {data && reconciliationData ? (
              <>
                <div
                  role="status"
                  className={`rounded-md border px-4 py-3 ${disabled
                    ? "border-destructive/30 bg-destructive/5 text-destructive"
                    : healthy
                      ? "border-success/30 bg-success/5 text-success"
                      : "border-warning/30 bg-warning/5 text-warning"}`}
                >
                  <p className="font-medium">
                    {disabled
                      ? t(($) => $.clauseGovernance.banner.dynamicOffTitle)
                      : healthy
                        ? t(($) => $.clauseGovernance.banner.healthyTitle)
                        : t(($) => $.clauseGovernance.banner.findingsTitle, { count: findings })}
                  </p>
                  <p className="text-body">
                    {disabled
                      ? t(($) => $.clauseGovernance.banner.dynamicOffBody)
                      : healthy
                        ? t(($) => $.clauseGovernance.banner.healthyBody, {
                            degraded: reconciliationData.totals.degraded_rows,
                            total: reconciliationData.totals.injections,
                          })
                        : t(($) => $.clauseGovernance.banner.findingsBody)}
                  </p>
                </div>
                <section aria-label={t(($) => $.clauseGovernance.metrics.injections)} className="grid grid-cols-1 gap-3 lg:grid-cols-2 xl:grid-cols-4">
                  <MetricCard title={t(($) => $.clauseGovernance.metrics.injections)} value={numberLabel(data.totals.injections)} note={t(($) => $.clauseGovernance.metrics.runsUnit, { count: data.totals.runs })} />
                  <MetricCard title={t(($) => $.clauseGovernance.metrics.clauseTokens)} value={numberLabel(data.totals.clause_tokens)} note={t(($) => $.clauseGovernance.metrics.clauseTokensNote)} />
                  <MetricCard title={t(($) => $.clauseGovernance.metrics.degraded)} value={numberLabel(data.totals.degraded_rows)} note={`${data.totals.degraded_rows}/${data.totals.injections} · ${numberLabel(data.totals.degraded_runs)} ${t(($) => $.clauseGovernance.metrics.runs)}`} />
                  <Card>
                    <CardHeader className="pb-2"><CardTitle className="text-body">{t(($) => $.clauseGovernance.metrics.migration)}</CardTitle></CardHeader>
                    <CardContent className="space-y-2">
                      <div className="flex justify-between text-title font-semibold">
                        <span>{numberLabel(data.migration.bound_carriers)} / {numberLabel(data.migration.total_carriers)}</span>
                      </div>
                      <Progress value={data.migration.total_carriers === 0 ? 0 : Math.min(100, data.migration.bound_carriers / data.migration.total_carriers * 100)} aria-label={t(($) => $.clauseGovernance.metrics.migration)} />
                      <p className="text-caption text-muted-foreground">
                        {Object.entries(data.migration.by_scope).map(([scope, count]) =>
                          `${enumLabel(scope, ["workspace", "project", "squad", "agent"], (key) => t(($) => $.clauseGovernance.scope[key]))} ${count}/${data.migration.by_scope_total[scope] ?? 0}`,
                        ).join(" · ")}
                      </p>
                    </CardContent>
                  </Card>
                </section>
                <ReconciliationSection data={reconciliationData} clauseById={new Map(data.clauses.map((clause) => [clause.id, clause]))} days={days} />
                <RollbackSection suggestions={data.rollback_suggestions} paths={paths} />
                <section aria-labelledby="clause-list-title" className="space-y-3">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <h2 id="clause-list-title" className="text-heading font-semibold">{t(($) => $.clauseGovernance.list.title)}</h2>
                    <div className="flex flex-wrap gap-1" role="group" aria-label={t(($) => $.clauseGovernance.list.title)}>
                      {(["all", "active", "deprecated", "revoked"] as const).map((status) => (
                        <Button
                          key={status}
                          size="sm"
                          variant={statusFilter === status ? "secondary" : "ghost"}
                          aria-pressed={statusFilter === status}
                          onClick={() => setStatusFilter(status)}
                        >
                          {t(($) => $.clauseGovernance.list[`filter_${status}`])}
                          <span className="ml-1 text-caption text-muted-foreground">{status === "all" ? data.clauses.length : data.status_counts[status] ?? 0}</span>
                        </Button>
                      ))}
                    </div>
                  </div>
                  {data.clauses.length === 0 ? (
                    <Card><CardContent className="flex flex-col items-center gap-2 py-8 text-center">
                      <h3 className="font-medium">{t(($) => $.clauseGovernance.list.emptyTitle)}</h3>
                      <p className="text-body text-muted-foreground">{t(($) => $.clauseGovernance.list.emptyBody)}</p>
                      <AppLink href={paths.selfEvolutionProposals()} className={buttonVariants({ variant: "outline" })}>{t(($) => $.clauseGovernance.list.emptyCta)}</AppLink>
                    </CardContent></Card>
                  ) : (
                    <div className="overflow-x-auto rounded-md border">
                      <table className="w-full min-w-[720px] text-body">
                        <thead className="bg-muted/50 text-left text-caption text-muted-foreground">
                          <tr>
                            <th scope="col" className="px-3 py-2">{t(($) => $.clauseGovernance.list.colStatus)}</th>
                            <th scope="col" className="px-3 py-2"><SortButton label={t(($) => $.clauseGovernance.list.colClause)} active={sort.field === "title"} desc={sort.desc} onClick={() => cycleSort("title")} /></th>
                            <th scope="col" className="px-3 py-2">{t(($) => $.clauseGovernance.list.colVersion)}</th>
                            <th scope="col" className="px-3 py-2">{t(($) => $.clauseGovernance.list.colScope)}</th>
                            <th scope="col" className="px-3 py-2 text-right"><SortButton label={t(($) => $.clauseGovernance.list.colInjections)} active={sort.field === "injections"} desc={sort.desc} onClick={() => cycleSort("injections")} /></th>
                            <th scope="col" className="px-3 py-2 text-right"><SortButton label={t(($) => $.clauseGovernance.list.colRuns)} active={sort.field === "runs"} desc={sort.desc} onClick={() => cycleSort("runs")} /></th>
                            <th scope="col" className="px-3 py-2 text-right"><SortButton label={t(($) => $.clauseGovernance.list.colTokens)} active={sort.field === "injected_tokens"} desc={sort.desc} onClick={() => cycleSort("injected_tokens")} /></th>
                            <th scope="col" className="px-3 py-2 text-right">{t(($) => $.clauseGovernance.list.colAction)}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {clauses.map((clause) => <ClauseRow key={clause.id} clause={clause} days={days} paths={paths} />)}
                          {data.clauses.length > 0 && clauses.length === 0 ? <tr><td colSpan={8} className="px-3 py-8 text-center text-muted-foreground">{t(($) => $.clauseGovernance.list.noFilterResults)}</td></tr> : null}
                        </tbody>
                      </table>
                    </div>
                  )}
                </section>
              </>
            ) : null}
          </>
        )}
      </main>
  );
}

function MetricCard({ title, value, note }: { title: string; value: string; note: string }) {
  return <Card><CardHeader className="pb-2"><CardTitle className="text-body">{title}</CardTitle></CardHeader><CardContent><div className="text-title font-semibold">{value}</div><p className="text-caption text-muted-foreground">{note}</p></CardContent></Card>;
}

function OverviewSkeleton() {
  return <div className="space-y-4" aria-hidden="true">
    <Skeleton className="h-16 w-full" />
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-2 xl:grid-cols-4">{[0, 1, 2, 3].map((item) => <Skeleton key={item} className="h-28" />)}</div>
    <Skeleton className="h-64 w-full" /><Skeleton className="h-72 w-full" />
  </div>;
}

function QueryError({ retry }: { retry: () => void }) {
  const { t } = useT("self-evolution");
  return <div role="alert" className="flex items-center justify-between rounded-md border border-destructive/30 bg-destructive/5 px-4 py-3 text-destructive">
    <span>{t(($) => $.clauseGovernance.error.title)}</span>
    <Button variant="outline" size="sm" onClick={retry}>{t(($) => $.clauseGovernance.error.retry)}</Button>
  </div>;
}

function SortButton({ label, active, desc, onClick }: { label: string; active: boolean; desc: boolean; onClick: () => void }) {
  return <button type="button" onClick={onClick} aria-label={label} className="font-medium hover:text-foreground">
    {label}{active ? (desc ? " ↓" : " ↑") : null}
  </button>;
}

function ClauseRow({ clause, days, paths }: { clause: ClauseGovernanceClause; days: number; paths: ReturnType<typeof useWorkspacePaths> }) {
  const { t } = useT("self-evolution");
  const detailPath = `${paths.selfEvolutionClauseGovernanceDetail(clause.id)}?days=${days}`;
  return <tr className="border-t hover:bg-muted/30">
    <td className="px-3 py-3"><Badge variant="outline" className={statusClass(clause.status)}>{enumLabel(clause.status, ["active", "deprecated", "revoked"], (key) => t(($) => $.clauseGovernance.status[key]))}</Badge></td>
    <td className="px-3 py-3"><AppLink href={detailPath} className="font-medium text-foreground hover:underline">{clause.title}<span className="block font-mono text-caption text-muted-foreground">{clause.slug}</span></AppLink></td>
    <td className="px-3 py-3"><Badge variant="secondary">V{clause.latest_version}</Badge></td>
    <td className="px-3 py-3">{clause.bound_scope ? <ScopeBadge scope={clause.bound_scope} label={enumLabel(clause.bound_scope, ["workspace", "project", "squad", "agent"], (key) => t(($) => $.clauseGovernance.scope[key]))} /> : <span className="text-muted-foreground">{t(($) => $.clauseGovernance.list.unbound)}</span>}</td>
    <td className="px-3 py-3 text-right tabular-nums">{numberLabel(clause.injections)}</td>
    <td className="px-3 py-3 text-right tabular-nums">{numberLabel(clause.runs)}</td>
    <td className="px-3 py-3 text-right tabular-nums">{clause.tokens_pending === true ? <Badge variant="outline" title={t(($) => $.clauseGovernance.list.tokensPendingHint)} className="text-muted-foreground">{t(($) => $.clauseGovernance.list.tokensPending)}</Badge> : numberLabel(clause.injected_tokens)}</td>
    <td className="px-3 py-3 text-right"><AppLink href={detailPath} className="text-primary hover:underline">{t(($) => $.clauseGovernance.list.openDetail)}</AppLink></td>
  </tr>;
}

function ReconciliationSection({ data, clauseById, days }: { data: ClauseGovernanceReconciliation; clauseById: Map<string, ClauseGovernanceClause>; days: number }) {
  const { t } = useT("self-evolution");
  const paths = useWorkspacePaths();
  const violationRows = data.layer_set_violations;
  const findingCount = violationRows.length + data.drift.reduce((count, carrier) => count + carrier.entries.length, 0);
  return <Card>
    <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 space-y-0">
      <CardTitle>{t(($) => $.clauseGovernance.recon.title)}</CardTitle>
      <Badge variant="outline" className={data.healthy ? "border-transparent bg-success/10 text-success dark:bg-success/20" : "border-transparent bg-warning/10 text-warning dark:bg-warning/20"}>
        {data.healthy ? <CheckCircle2 aria-hidden className="mr-1 size-3" /> : <AlertTriangle aria-hidden className="mr-1 size-3" />}
        {data.healthy ? t(($) => $.clauseGovernance.recon.healthy) : t(($) => $.clauseGovernance.recon.findings, { count: findingCount })}
      </Badge>
      <p className="w-full text-caption text-muted-foreground">{t(($) => $.clauseGovernance.recon.scanned, { count: data.carriers_scanned })} · {new Date(data.since).toLocaleDateString()}</p>
    </CardHeader>
    <CardContent className="grid min-w-0 grid-cols-1 gap-5 xl:grid-cols-2">
      <section className="min-w-0 space-y-2">
        <h3 className="font-medium">{t(($) => $.clauseGovernance.recon.violationsTitle)}</h3>
        {violationRows.length === 0 ? <p className="text-body text-muted-foreground">{t(($) => $.clauseGovernance.recon.violationsEmpty)}</p> : <div className="overflow-x-auto"><table className="w-full min-w-[720px] text-body">
          <thead><tr className="text-left text-caption text-muted-foreground"><th className="px-2 py-2">{t(($) => $.clauseGovernance.recon.colTime)}</th><th className="px-2 py-2">{t(($) => $.clauseGovernance.recon.colTask)}</th><th className="px-2 py-2">{t(($) => $.clauseGovernance.recon.colClause)}</th><th className="px-2 py-2">{t(($) => $.clauseGovernance.recon.colVersion)}</th><th className="px-2 py-2">{t(($) => $.clauseGovernance.recon.colScope)}</th><th className="px-2 py-2">{t(($) => $.clauseGovernance.recon.colInjection)}</th></tr></thead>
          <tbody>{violationRows.slice(0, 100).map((row, index) => {
            const clause = clauseById.get(row.clause_id);
            return <tr key={`${row.task_id}-${index}`} className="border-t"><td className="px-2 py-2">{new Date(row.created_at).toLocaleString()}</td><td className="px-2 py-2 font-mono" title={row.task_id}>{shortId(row.task_id)}</td><td className="px-2 py-2">{clause ? <AppLink href={`${paths.selfEvolutionClauseGovernanceDetail(clause.id)}?days=${days}`} className="font-mono text-primary hover:underline" title={clause.title}>{clause.slug}</AppLink> : <span className="font-mono" title={row.clause_id}>{shortId(row.clause_id)}</span>}</td><td className="px-2 py-2">V{row.clause_version}</td><td className="px-2 py-2">{enumLabel(row.carrier_scope, ["workspace", "project", "squad", "agent"], (key) => t(($) => $.clauseGovernance.scope[key]))}</td><td className="px-2 py-2">{row.degraded === true ? <Badge variant="outline" className="border-transparent bg-warning/10 text-warning dark:bg-warning/20">{t(($) => $.clauseGovernance.recon.degradedFlag)}</Badge> : null}</td></tr>;
          })}</tbody>
        </table></div>}
        {violationRows.length >= 100 ? <p className="text-caption text-muted-foreground">{t(($) => $.clauseGovernance.recon.truncated)}</p> : null}
      </section>
      <section className="space-y-2">
        <h3 className="font-medium">{t(($) => $.clauseGovernance.recon.driftTitle)}</h3>
        {data.drift.length === 0 ? <p className="flex items-center gap-2 text-body text-success"><CheckCircle2 aria-hidden className="size-4" />{t(($) => $.clauseGovernance.recon.driftEmpty, { count: data.carriers_scanned })}</p> : <div className="grid gap-3 sm:grid-cols-2">
          {data.drift.map((carrier) => <Card key={`${carrier.scope}-${carrier.scope_id}`} className="min-w-0">
            <CardHeader className="pb-2"><CardTitle className="flex flex-wrap items-center gap-2 text-body"><ScopeBadge scope={carrier.scope} label={enumLabel(carrier.scope, ["workspace", "project", "squad", "agent"], (key) => t(($) => $.clauseGovernance.scope[key]))} /> <span className="font-mono text-caption text-muted-foreground" title={carrier.scope_id}>{shortId(carrier.scope_id)}</span></CardTitle></CardHeader>
            <CardContent className="space-y-2">{carrier.entries.map((entry, index) => <div key={`${entry.slug}-${index}`} className="space-y-1 border-t pt-2 first:border-0 first:pt-0">
              <Badge variant="outline" className={`inline-flex items-center gap-1 ${entry.kind === "stale_rev" ? "border-transparent bg-warning/10 text-warning dark:bg-warning/20" : "border-destructive/30 text-destructive"}`}><AlertTriangle aria-hidden className="size-3" />{enumLabel(entry.kind, ["stale_rev", "orphan_marker", "missing_segment"], (key) => t(($) => $.clauseGovernance.driftKind[key]))}</Badge>
              <p className="font-mono text-caption">{entry.slug}</p>
              <p className="text-caption text-muted-foreground">{entry.kind === "stale_rev"
                ? t(($) => $.clauseGovernance.driftDesc.staleRev, { marker: entry.marker_rev ?? "—", expected: entry.expected_rev ?? "—" })
                : entry.kind === "orphan_marker"
                  ? t(($) => $.clauseGovernance.driftDesc.orphanMarker)
                  : t(($) => $.clauseGovernance.driftDesc.missingSegment, { expected: entry.expected_rev ?? "—" })}</p>
            </div>)}</CardContent>
          </Card>)}
        </div>}
      </section>
    </CardContent>
  </Card>;
}

function RollbackSection({ suggestions, paths }: { suggestions: ClauseGovernanceDetail["proposals"]; paths: ReturnType<typeof useWorkspacePaths> }) {
  const { t } = useT("self-evolution");
  return <Card>
    <CardHeader><CardTitle>{t(($) => $.clauseGovernance.rollback.title)}</CardTitle></CardHeader>
    <CardContent className="space-y-3">
      {suggestions.length === 0 ? <p className="text-body text-muted-foreground">{t(($) => $.clauseGovernance.rollback.empty)}</p> : suggestions.map((proposal) => <div key={proposal.id} className="flex flex-wrap items-center gap-2 border-t pt-3 first:border-0 first:pt-0">
        <span className="font-medium">{proposal.clause_name}</span><Badge variant="outline">{enumLabel(proposal.change_kind, ["add_clause", "remove_clause"], (key) => t(($) => $.legislation.changeKind[key]))}</Badge>
        {proposal.rollback_of_version !== undefined ? <span className="text-caption">{t(($) => $.clauseGovernance.rollback.rollbackTo, { version: proposal.rollback_of_version })}</span> : null}
        <span className="min-w-0 flex-1 truncate text-caption text-muted-foreground" title={proposal.rollback_reason}>{proposal.rollback_reason}</span>
        <Badge variant="secondary">{enumLabel(proposal.status, ["draft", "pending_owner", "approved", "enacted", "gate_failed", "rejected"], (key) => t(($) => $.legislation.status[key]))}</Badge>
        <AppLink className="text-primary hover:underline" href={paths.selfEvolutionProposals()}>{t(($) => $.clauseGovernance.rollback.viewProposal)}</AppLink>
      </div>)}
    </CardContent>
  </Card>;
}

export function ClauseGovernanceDetailPage({ clauseId }: { clauseId: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("self-evolution");
  const navigation = useNavigation();
  const paths = useWorkspacePaths();
  const days = readDays(navigation.searchParams);
  const query = useQuery(clauseGovernanceDetailOptions(wsId, clauseId, days));
  const backPath = `${paths.selfEvolutionClauseGovernance()}?days=${days}`;
  return <SelfEvolutionShell>
    <main className="flex min-w-0 flex-col gap-4 px-1 pb-8" data-testid="clause-governance-detail">
      <AppLink href={backPath} className="inline-flex w-fit items-center gap-2 text-body text-muted-foreground hover:text-foreground"><ArrowLeft aria-hidden className="size-4" />{t(($) => $.clauseGovernance.detail.back)}</AppLink>
      {query.isLoading ? <Skeleton className="h-16 w-full" /> : query.isError || !query.data?.id ? <Card><CardContent className="flex flex-col items-center gap-3 py-10 text-center">
        <h1 className="text-heading font-semibold">{query.isError ? t(($) => $.clauseGovernance.error.title) : t(($) => $.clauseGovernance.detail.notFound)}</h1>
        {query.isError ? <Button variant="outline" onClick={() => void query.refetch()}>{t(($) => $.clauseGovernance.error.retry)}</Button> : null}
        <AppLink href={backPath} className={buttonVariants({ variant: "outline" })}>{t(($) => $.clauseGovernance.detail.back)}</AppLink>
      </CardContent></Card> : <DetailContents data={query.data} days={days} paths={paths} />}
    </main>
  </SelfEvolutionShell>;
}

function DetailContents({ data, days, paths }: { data: ClauseGovernanceDetail; days: number; paths: ReturnType<typeof useWorkspacePaths> }) {
  const { t } = useT("self-evolution");
  const qualityUnmeasured = data.quality_daily.length > 0 && data.quality_daily.every((row) => row.injected_tokens == null);
  return <>
    <header className="flex flex-wrap items-start justify-between gap-4">
      <div><h1 className="text-title font-semibold">{data.title}</h1><p className="font-mono text-body text-muted-foreground">{data.slug}</p>
        <div className="mt-2 flex flex-wrap items-center gap-2"><Badge variant="outline" className={statusClass(data.status)}>{enumLabel(data.status, ["active", "deprecated", "revoked"], (key) => t(($) => $.clauseGovernance.status[key]))}</Badge><Badge variant="secondary">V{data.latest_version} · {t(($) => $.clauseGovernance.detail.latest)}</Badge></div>
      </div>
      <div className="flex flex-wrap gap-2 text-caption">
        <Badge variant="outline">{t(($) => $.clauseGovernance.metrics.injections)} · {numberLabel(data.injections)}</Badge>
        <Badge variant="outline">{t(($) => $.clauseGovernance.metrics.runs)} · {numberLabel(data.runs)}</Badge>
        <Badge variant="outline">{t(($) => $.clauseGovernance.metrics.clauseTokens)} · {qualityUnmeasured ? t(($) => $.clauseGovernance.quality.unmeasured) : numberLabel(data.injected_tokens)}</Badge>
      </div>
    </header>
    <Tabs defaultValue="versions" className="min-w-0">
      <TabsList className="flex h-auto w-full flex-wrap justify-start">
        <TabsTrigger value="versions">{t(($) => $.clauseGovernance.detail.versions)}</TabsTrigger>
        <TabsTrigger value="bindings">{t(($) => $.clauseGovernance.detail.bindings)}</TabsTrigger>
        <TabsTrigger value="proposals">{t(($) => $.clauseGovernance.detail.proposals)}</TabsTrigger>
        <TabsTrigger value="quality">{t(($) => $.clauseGovernance.detail.quality)}</TabsTrigger>
      </TabsList>
      <TabsContent value="versions"><VersionsPanel versions={data.versions} quality={data.quality_daily} /></TabsContent>
      <TabsContent value="bindings"><BindingsPanel bindings={data.bindings} /></TabsContent>
      <TabsContent value="proposals"><ProposalsPanel proposals={data.proposals} paths={paths} /></TabsContent>
      <TabsContent value="quality"><QualityPanel quality={data.quality_daily} days={days} /></TabsContent>
    </Tabs>
  </>;
}

function VersionsPanel({ versions, quality }: { versions: ClauseGovernanceVersion[]; quality: ClauseGovernanceQualityDay[] }) {
  const { t } = useT("self-evolution");
  const [compareA, setCompareA] = useState("");
  const [compareB, setCompareB] = useState("");
  const [comparisonOpen, setComparisonOpen] = useState(false);
  const first = versions.find((version) => String(version.version) === compareA);
  const second = versions.find((version) => String(version.version) === compareB);
  const diff = first && second && first.version !== second.version
    ? diffPromptText(first.content, second.content)
    : null;
  const versionRows = (version: number) => quality.filter((row) => row.clause_version === version);
  const versionRuns = (version: number) => versionRows(version)
    .reduce((sum, row) => sum + row.finished_runs + row.attributable_failed_runs, 0);
  const versionFailures = (version: number) => versionRows(version)
    .reduce((sum, row) => sum + row.attributable_failed_runs, 0);
  const versionTokens = (version: number) => {
    const rows = versionRows(version);
    if (rows.length === 0 || rows.some((row) => row.injected_tokens == null)) return null;
    return rows.reduce((sum, row) => sum + (row.injected_tokens ?? 0), 0);
  };
  const comparisonTitle = first && second
    ? t(($) => $.clauseGovernance.compare.title, { a: first.version, b: second.version })
    : "";
  return <div className="space-y-4 pt-4">
    <Card><CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 space-y-0"><CardTitle>{t(($) => $.clauseGovernance.detail.versions)}</CardTitle>
      {versions.length > 1 ? <div className="flex flex-wrap items-center gap-2"><label className="text-caption">{t(($) => $.clauseGovernance.version.compare)} <select className="h-8 rounded-md border bg-background px-2" value={compareA} onChange={(event) => { setCompareA(event.target.value); setComparisonOpen(false); }}><option value="">—</option>{versions.map((v) => <option key={v.version} value={v.version}>V{v.version}</option>)}</select></label><span aria-hidden>↔</span><label className="sr-only" htmlFor="clause-version-compare-b">{t(($) => $.clauseGovernance.version.compare)}</label><select id="clause-version-compare-b" className="h-8 rounded-md border bg-background px-2" value={compareB} onChange={(event) => { setCompareB(event.target.value); setComparisonOpen(false); }}><option value="">—</option>{versions.map((v) => <option key={v.version} value={v.version}>V{v.version}</option>)}</select><Button size="sm" variant="outline" disabled={diff === null} onClick={() => setComparisonOpen(true)}>{t(($) => $.clauseGovernance.version.compare)}</Button></div> : null}
    </CardHeader><CardContent className="space-y-4">
      <ol className="space-y-4">{[...versions].sort((a, b) => b.version - a.version).map((version, index) => <li key={version.version} className="relative border-l pl-5">
        {index < versions.length - 1 ? <span aria-hidden className="absolute -left-px top-3 h-full border-l" /> : null}
        <span aria-hidden className="absolute -left-[5px] top-1 size-2.5 rounded-full bg-primary" />
        <div className="flex flex-wrap items-center gap-2"><Badge variant={index === 0 ? "default" : "outline"}>V{version.version}</Badge><span className="text-caption text-muted-foreground">{new Date(version.created_at).toLocaleString()}</span><Badge variant="outline">{version.source}</Badge><Badge variant="outline">{enumLabel(version.carrier_scope, ["workspace", "project", "squad", "agent"], (key) => t(($) => $.clauseGovernance.scope[key]))}</Badge><span className="text-caption text-muted-foreground">{version.created_by_type}</span></div>
        <p className="mt-2 text-caption text-muted-foreground">{t(($) => $.clauseGovernance.version.sha)} <code className="font-mono" title={version.content_sha256}>{version.content_sha256.slice(0, 8)}</code></p>
        <details className="mt-2 rounded-md border p-3"><summary className="cursor-pointer text-body">{t(($) => $.clauseGovernance.version.showContent)}</summary><pre className="mt-3 max-h-40 overflow-auto whitespace-pre-wrap font-mono text-caption">{version.content}</pre></details>
      </li>)}</ol>
    </CardContent></Card>
    {diff && first && second ? <Dialog open={comparisonOpen} onOpenChange={setComparisonOpen}>
      <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-5xl">
        <DialogHeader><DialogTitle>{comparisonTitle}</DialogTitle></DialogHeader>
        <table className="w-full text-body" aria-label={comparisonTitle}>
          <thead><tr className="border-b text-left text-caption text-muted-foreground"><th scope="col" className="px-3 py-2">{t(($) => $.clauseGovernance.detail.versions)}</th><th scope="col" className="px-3 py-2">{t(($) => $.clauseGovernance.compare.attributableRuns)}</th><th scope="col" className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colFailed)}</th><th scope="col" className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colTokens)}</th></tr></thead>
          <tbody>{[first, second].map((version) => {
            const runs = versionRuns(version.version);
            const tokens = versionTokens(version.version);
            return <tr key={version.version} className="border-b">
              <th scope="row" className="px-3 py-2 font-medium">V{version.version}</th>
              <td className="px-3 py-2">{numberLabel(runs)}{runs < 30 ? <Badge className="ml-2 border-transparent bg-warning/10 text-warning dark:bg-warning/20">{t(($) => $.clauseGovernance.compare.sampleInsufficient)}</Badge> : null}</td>
              <td className="px-3 py-2">{numberLabel(versionFailures(version.version))}</td>
              <td className="px-3 py-2">{tokens == null ? t(($) => $.clauseGovernance.quality.unmeasured) : numberLabel(tokens)}</td>
            </tr>;
          })}</tbody>
        </table>
        <div className="grid min-w-0 gap-3 lg:grid-cols-2">
          {[{ version: first, lines: diff.lines.filter((line) => line.kind !== "added") }, { version: second, lines: diff.lines.filter((line) => line.kind !== "removed") }].map(({ version, lines }) => <section key={version.version} className="min-w-0 space-y-2" aria-label={t(($) => $.quality.timeline.version, { version: version.version })}>
            <h3 className="font-medium">{t(($) => $.quality.timeline.version, { version: version.version })}</h3>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded bg-muted/40 p-3 font-mono text-caption">{lines.map((line, index) => <span key={`${index}-${line.kind}`} className={line.kind === "added" ? "block bg-success/10 dark:bg-success/15" : line.kind === "removed" ? "block bg-destructive/10 dark:bg-destructive/15" : "block"}>{line.kind === "added" ? "+ " : line.kind === "removed" ? "− " : "  "}{line.text}{"\n"}</span>)}</pre>
          </section>)}
        </div>
      </DialogContent>
    </Dialog> : null}
  </div>;
}

function BindingsPanel({ bindings }: { bindings: ClauseGovernanceBinding[] }) {
  const { t } = useT("self-evolution");
  const sorted = [...bindings].sort((a, b) => Number(b.status === "active") - Number(a.status === "active") || b.created_at.localeCompare(a.created_at));
  return <div className="overflow-x-auto pt-4"><table className="w-full min-w-[720px] text-body">
    <thead><tr className="text-left text-caption text-muted-foreground"><th className="px-3 py-2">{t(($) => $.clauseGovernance.binding.scope)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.binding.status)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.binding.conditions)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.binding.sinceVersion)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.binding.createdAt)}</th></tr></thead>
    <tbody>{sorted.map((binding) => <tr key={binding.id} className="border-t"><td className="px-3 py-3"><ScopeBadge scope={binding.scope} label={enumLabel(binding.scope, ["workspace", "project", "squad", "agent"], (key) => t(($) => $.clauseGovernance.scope[key]))} /> <code className="font-mono text-caption text-muted-foreground" title={binding.scope_id}>{shortId(binding.scope_id)}</code></td><td className="px-3 py-3"><Badge variant={binding.status === "superseded" ? "secondary" : "outline"} className={binding.status === "active" ? statusClass("active") : binding.status === "withdrawn" ? "text-muted-foreground" : ""}>{enumLabel(binding.status, ["active", "superseded", "withdrawn"], (key) => t(($) => $.clauseGovernance.bindingStatus[key]))}</Badge></td><td className="px-3 py-3">{binding.conditions == null || (binding.conditions.task_kinds?.length ?? 0) === 0 ? <span className="text-muted-foreground">{t(($) => $.clauseGovernance.binding.conditionsAll)}</span> : <div className="flex flex-wrap gap-1">{binding.conditions.task_kinds?.map((kind) => <Badge key={kind} variant="secondary">{kind}</Badge>)}</div>}</td><td className="px-3 py-3">{t(($) => $.clauseGovernance.binding.sinceVersion, { version: binding.since_clause_version })}</td><td className="px-3 py-3">{new Date(binding.created_at).toLocaleString()}</td></tr>)}</tbody>
  </table>{bindings.length === 0 ? <p className="py-8 text-center text-muted-foreground">{t(($) => $.clauseGovernance.binding.empty)}</p> : null}</div>;
}

function ProposalsPanel({ proposals, paths }: { proposals: ClauseGovernanceDetail["proposals"]; paths: ReturnType<typeof useWorkspacePaths> }) {
  const { t } = useT("self-evolution");
  return <Card className="mt-4"><CardContent className="space-y-3 pt-4">
    {proposals.length === 0 ? <p className="text-body text-muted-foreground">{t(($) => $.clauseGovernance.proposals.empty)}</p> : proposals.map((proposal) => <details key={proposal.id} className="rounded-md border p-3">
      <summary className="flex cursor-pointer flex-wrap items-center gap-2"><Badge variant="outline">{enumLabel(proposal.change_kind, ["add_clause", "remove_clause"], (key) => t(($) => $.legislation.changeKind[key]))}</Badge><span className="font-medium">{proposal.clause_name}</span><Badge variant="secondary">{enumLabel(proposal.status, ["draft", "pending_owner", "approved", "enacted", "gate_failed", "rejected"], (key) => t(($) => $.legislation.status[key]))}</Badge>{proposal.enacted_version !== undefined ? <span>{t(($) => $.quality.timeline.version, { version: proposal.enacted_version })}</span> : null}{proposal.rollback_of_version !== undefined ? <Badge className="border-transparent bg-warning/10 text-warning dark:bg-warning/20">{t(($) => $.clauseGovernance.rollback.rollbackTo, { version: proposal.rollback_of_version })}</Badge> : null}<span className="ml-auto text-caption text-muted-foreground">{new Date(proposal.created_at).toLocaleString()}</span></summary>
      <dl className="mt-3 grid gap-2 text-body sm:grid-cols-2">{(["layer", "retention", "cost", "conflict", "dedup"] as const).map((gate) => <div key={gate}><dt className="text-caption text-muted-foreground">{t(($) => $.clauseGovernance.proposals.gates[gate])}</dt><dd className="line-clamp-2">{proposal[`gate_answer_${gate}`]}</dd></div>)}</dl>
    </details>)}
    <AppLink href={paths.selfEvolutionProposals()} className="inline-block text-primary hover:underline">{t(($) => $.clauseGovernance.rollback.viewProposal)}</AppLink>
  </CardContent></Card>;
}

function QualityPanel({ quality, days }: { quality: ClauseGovernanceQualityDay[]; days: number }) {
  const { t } = useT("self-evolution");
  const rows = [...quality].sort((a, b) => a.day.localeCompare(b.day));
  const chartConfig = {
    finished_runs: { label: t(($) => $.clauseGovernance.quality.colFinishedRuns), color: "var(--chart-1)" },
    attributable_failed_runs: { label: t(($) => $.clauseGovernance.quality.colFailed), color: "var(--chart-5)" },
    injected_tokens: { label: t(($) => $.clauseGovernance.quality.colTokens), color: "var(--chart-2)" },
  };
  return <section className="space-y-3 pt-4">
    <Card><CardHeader><CardTitle>{t(($) => $.clauseGovernance.quality.chartTitle)}</CardTitle></CardHeader><CardContent>
      {rows.length === 0 ? <p className="text-body text-muted-foreground">{t(($) => $.clauseGovernance.quality.emptyWindow)}</p> : <>
        <ChartContainer config={chartConfig} className="aspect-[3/1] w-full" role="img" aria-label={t(($) => $.clauseGovernance.quality.chartAria, { days })}>
          <LineChart data={rows} margin={{ left: 0, right: 0, top: 12, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="day" tickLine={false} axisLine={false} tickMargin={8} />
            <YAxis yAxisId="runs" tickLine={false} axisLine={false} width={32} />
            <YAxis yAxisId="tokens" orientation="right" tickLine={false} axisLine={false} width={32} />
            <ChartTooltip content={<ChartTooltipContent labelFormatter={(label) => String(label)} />} />
            {rows.map((row, index) => index > 0 && rows[index - 1]?.clause_version !== row.clause_version
              ? <ReferenceLine key={`${row.day}-${row.clause_version}`} x={row.day} stroke="var(--border)" label={`V${row.clause_version}`} />
              : null)}
            <Line yAxisId="runs" dataKey="finished_runs" stroke="var(--color-finished_runs)" strokeWidth={2} dot={false} isAnimationActive={false} />
            <Line yAxisId="runs" dataKey="attributable_failed_runs" stroke="var(--color-attributable_failed_runs)" strokeWidth={2} dot={false} isAnimationActive={false} />
            <Line yAxisId="tokens" dataKey="injected_tokens" stroke="var(--color-injected_tokens)" strokeWidth={2} dot={false} isAnimationActive={false} connectNulls={false} />
          </LineChart>
        </ChartContainer>
        <ul className="flex flex-wrap gap-x-4 gap-y-1 text-caption text-muted-foreground" aria-hidden="true">
          {Object.entries(chartConfig).map(([key, series]) => <li key={key} className="flex items-center gap-1.5"><span className="size-2 rounded-full" style={{ backgroundColor: series.color }} />{series.label}</li>)}
        </ul>
      </>}
    </CardContent></Card>
    {rows.length > 0 ? <div className="overflow-x-auto rounded-md border"><table className="w-full min-w-[720px] text-body">
      <thead><tr className="text-left text-caption text-muted-foreground"><th className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colDay)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colVersion)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colFinishedRuns)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colFailed)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colExcluded)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colTokens)}</th><th className="px-3 py-2">{t(($) => $.clauseGovernance.quality.colMedianRun)}</th></tr></thead>
      <tbody>{rows.map((row) => <tr key={`${row.day}-${row.clause_version}`} className="border-t"><td className="px-3 py-2">{row.day}</td><td className="px-3 py-2"><Badge variant="outline">V{row.clause_version}</Badge></td><td className="px-3 py-2">{numberLabel(row.finished_runs)}</td><td className="px-3 py-2">{numberLabel(row.attributable_failed_runs)}</td><td className="px-3 py-2">{numberLabel(row.excluded_failed_runs)}</td><td className="px-3 py-2">{row.injected_tokens == null ? <span className="text-muted-foreground">{t(($) => $.clauseGovernance.quality.unmeasured)}</span> : numberLabel(row.injected_tokens)}</td><td className="px-3 py-2">{row.run_tokens_median == null ? <span className="text-muted-foreground">{t(($) => $.clauseGovernance.quality.unmeasured)}</span> : numberLabel(row.run_tokens_median)}</td></tr>)}</tbody>
    </table></div> : null}
  </section>;
}
