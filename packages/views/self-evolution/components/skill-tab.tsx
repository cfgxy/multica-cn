"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RotateCcw } from "lucide-react";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  skillEffectOptions,
  skillUsageOptions,
  skillVersionOptions,
  skillVersionsOptions,
  useRestoreSkillVersion,
} from "@multica/core/self-evolution";
import { skillListOptions } from "@multica/core/workspace/queries";
import type { SkillVersionSummary } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@multica/ui/components/ui/alert-dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@multica/ui/components/ui/sheet";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { useT, useLocale } from "../../i18n";
import { AppLink } from "../../navigation";
import { PromptDiffView } from "../../market/prompt-diff-view";

export function SkillTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const [selectedId, setSelectedId] = useState("");
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null);
  const [comparisonVersion, setComparisonVersion] = useState<number | null>(null);
  const [confirmRestore, setConfirmRestore] = useState(false);
  const skills = useQuery(skillListOptions(wsId));
  const currentId = selectedId || skills.data?.[0]?.id || "";
  const currentSkill = skills.data?.find((s) => s.id === currentId);
  const versions = useQuery(skillVersionsOptions(wsId, currentId));
  const usage = useQuery(skillUsageOptions(wsId, currentId));
  const effect = useQuery(skillEffectOptions(wsId, currentId));
  const detail = useQuery(skillVersionOptions(wsId, currentId, selectedVersion ?? 0));
  const previous = useQuery(skillVersionOptions(wsId, currentId, (selectedVersion ?? 0) - 1));
  const restore = useRestoreSkillVersion(wsId, currentId);
  const latestVersion = versions.data?.[0]?.version;
  const olderVersions = versions.data?.slice(1) ?? [];
  const baselineVersion = olderVersions.some((v) => v.version === comparisonVersion)
    ? comparisonVersion : olderVersions[0]?.version;
  const currentUsage = usage.data?.versions.find((v) => v.version === latestVersion);
  const baselineUsage = usage.data?.versions.find((v) => v.version === baselineVersion);
  const sourceLabel = (source: string) => {
    switch (source) {
      case "create": return t(($) => $.skills.sources.create);
      case "proposal": return t(($) => $.skills.sources.proposal);
      case "revision": return t(($) => $.skills.sources.revision);
      case "edit": return t(($) => $.skills.sources.edit);
      case "restore": return t(($) => $.skills.sources.restore);
      default: return source;
    }
  };
  const windowLabel = (mode: string, days: number, uses: number) => {
    switch (mode) {
      case "uses": return t(($) => $.skills.windowModeUses, { uses });
      case "none": return t(($) => $.skills.windowModeNone);
      default: return t(($) => $.skills.windowModeDays, { days });
    }
  };

  if (skills.isPending) return <div className="space-y-4"><Skeleton className="h-9 w-56" /><Skeleton className="h-48 w-full" /></div>;
  if (skills.isError) return <p role="alert" className="text-body text-destructive">{t(($) => $.skills.error)} <Button variant="outline" size="sm" onClick={() => skills.refetch()}>{t(($) => $.skills.retry)}</Button></p>;
  if (!skills.data?.length) return <div className="space-y-3 text-body"><p>{t(($) => $.skills.empty)}</p><AppLink href={paths.skills()}>{t(($) => $.skills.manage)}</AppLink></div>;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <label className="space-y-1.5 text-caption text-muted-foreground">
          <span>{t(($) => $.skills.choose)}</span>
          <Select items={(skills.data ?? []).map((s) => ({ value: s.id, label: s.name }))} value={currentId} onValueChange={(id) => { if (typeof id === "string") { setSelectedId(id); setSelectedVersion(null); setComparisonVersion(null); } }}>
            <SelectTrigger className="w-56 max-w-full" size="sm"><SelectValue>{currentSkill?.name ?? t(($) => $.skills.choose)}</SelectValue></SelectTrigger>
            <SelectContent>{skills.data?.map((skill) => <SelectItem key={skill.id} value={skill.id}>{skill.name}</SelectItem>)}</SelectContent>
          </Select>
        </label>
        <Button size="sm" variant="outline" render={<AppLink href={paths.skills()} />}>{t(($) => $.skills.manage)}</Button>
      </div>

      <div className="flex flex-wrap items-center gap-3 border-b pb-4">
        <h2 className="text-title font-semibold">{currentSkill?.name}</h2>
        {versions.data?.[0] && <Badge variant="secondary">{t(($) => $.skills.version, { version: versions.data[0].version })}</Badge>}
        <p className="w-full text-body text-muted-foreground">{currentSkill?.description}</p>
      </div>

      <div className="grid min-w-0 gap-8 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
        <section className="min-w-0 space-y-3" aria-label={t(($) => $.skills.history)}>
          <h3 className="text-title font-medium">{t(($) => $.skills.history)}</h3>
          {versions.isPending ? <Skeleton className="h-40 w-full" /> : versions.isError ?
            <p role="alert" className="text-body text-destructive">{t(($) => $.skills.error)} <Button size="sm" onClick={() => versions.refetch()}>{t(($) => $.skills.retry)}</Button></p> :
            !versions.data?.length ? <p className="text-body text-muted-foreground">{t(($) => $.skills.noVersions)}</p> :
            <div className="divide-y border-y">{versions.data.map((v: SkillVersionSummary) => (
              <button key={v.id} type="button" className="flex w-full min-w-0 items-center gap-3 py-3 text-left text-body hover:bg-muted/50" onClick={() => setSelectedVersion(v.version)}>
                <Badge variant="outline">{t(($) => $.skills.version, { version: v.version })}</Badge>
                <span className="min-w-0 flex-1 truncate">{sourceLabel(v.source)}</span>
                <span className="text-caption text-muted-foreground">{usage.data?.versions.find((item) => item.version === v.version)?.count ?? "—"}</span>
              </button>
            ))}</div>}
        </section>
        <section className="min-w-0 space-y-4" aria-label={t(($) => $.skills.usage)}>
          <h3 className="text-title font-medium">{t(($) => $.skills.usage)}</h3>
          {usage.isPending ? <Skeleton className="h-32 w-full" /> : usage.isError || !usage.data ?
            <p role="alert" className="text-body text-destructive">{t(($) => $.skills.error)} <Button size="sm" onClick={() => usage.refetch()}>{t(($) => $.skills.retry)}</Button></p> : <>
            <div className="flex flex-wrap gap-x-10 gap-y-3 text-body">
              <div><p className="text-caption text-muted-foreground">{t(($) => $.skills.allTime)}</p><strong className="text-title">{t(($) => $.skills.invocations, { count: usage.data?.total ?? 0 })}</strong></div>
              <div><p className="text-caption text-muted-foreground">{t(($) => $.skills.last30)}</p><strong className="text-title">{usage.data?.last_30_days ?? 0}</strong></div>
              <div><p className="text-caption text-muted-foreground">{t(($) => $.skills.assigned)}</p><strong className="text-title">{usage.data?.assigned_agents ?? 0}</strong></div>
            </div>
            <p className="text-caption text-muted-foreground">{usage.data?.since ? t(($) => $.skills.observedSince, { date: new Date(usage.data.since).toLocaleDateString(locale) }) : t(($) => $.skills.noObserved)}</p>
            <h4 className="text-body font-medium">{t(($) => $.skills.versionCost)}</h4>
            {!usage.data?.versions.length ? <p className="text-body text-muted-foreground">{t(($) => $.skills.noObserved)}</p> :
              <div className="divide-y border-y">{usage.data.versions.map((item) => (
                <div key={item.version} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2 text-caption">
                  <span>{t(($) => $.skills.version, { version: item.version })}</span>
                  <span className="text-muted-foreground">{item.runs === undefined ? t(($) => $.skills.notCollected) : t(($) => $.skills.measuredRuns, { count: item.runs, samples: item.token_samples ?? 0 })}</span>
                  {item.token_samples === undefined ? <Badge variant="secondary">{t(($) => $.skills.notCollected)}</Badge> :
                    item.token_samples === 0 ? <Badge variant="secondary">{t(($) => $.skills.noCostData)}</Badge> :
                    item.token_samples < 5 || item.median_total_tokens == null ? <Badge variant="secondary">{t(($) => $.skills.insufficientCost)}</Badge> :
                      <strong>{t(($) => $.skills.medianTokens, { count: item.median_total_tokens })}</strong>}
                </div>
              ))}</div>}
            <div className="space-y-3 border-t pt-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h4 className="text-body font-medium">{t(($) => $.skills.versionComparison)}</h4>
                {olderVersions.length > 0 && baselineVersion !== undefined && <label className="flex items-center gap-2 text-caption text-muted-foreground">
                  {t(($) => $.skills.compareTo)}
                  <Select items={olderVersions.map((v) => ({ value: String(v.version), label: `v${v.version}` }))} value={String(baselineVersion)} onValueChange={(value) => { if (typeof value === "string") setComparisonVersion(Number(value)); }}>
                    <SelectTrigger size="sm" className="w-24"><SelectValue>v{baselineVersion}</SelectValue></SelectTrigger>
                    <SelectContent>{olderVersions.map((v) => <SelectItem key={v.id} value={String(v.version)}>v{v.version}</SelectItem>)}</SelectContent>
                  </Select>
                </label>}
              </div>
              {latestVersion === undefined || baselineVersion === undefined ?
                <p className="text-caption text-muted-foreground">{t(($) => $.skills.noComparison)}</p> :
                <div className="min-w-0 overflow-x-auto">
                  <div className="min-w-[28rem] space-y-2 text-caption">
                  <div className="grid grid-cols-[minmax(0,1fr)_repeat(2,minmax(0,6rem))_minmax(0,7rem)] gap-2 border-b pb-2 text-muted-foreground">
                    <span>{t(($) => $.skills.metric)}</span><span>v{baselineVersion}</span><span>v{latestVersion}</span><span>{t(($) => $.skills.change)}</span>
                  </div>
                  <SkillComparisonRow label={t(($) => $.skills.tokenMetric)}
                    before={baselineUsage?.median_total_tokens} after={currentUsage?.median_total_tokens}
                    beforeSamples={baselineUsage?.token_samples ?? (baselineUsage ? undefined : 0)}
                    afterSamples={currentUsage?.token_samples ?? (currentUsage ? undefined : 0)}
                    unit="token" insufficient={t(($) => $.skills.insufficientCost)}
                    noData={t(($) => $.skills.noCostData)} notCollected={t(($) => $.skills.notCollected)} />
                  <SkillComparisonRow label={t(($) => $.skills.retryMetric)}
                    before={baselineUsage?.runs && baselineUsage.retried_runs !== undefined ? baselineUsage.retried_runs / baselineUsage.runs * 100 : null}
                    after={currentUsage?.runs && currentUsage.retried_runs !== undefined ? currentUsage.retried_runs / currentUsage.runs * 100 : null}
                    beforeSamples={baselineUsage?.runs ?? (baselineUsage ? undefined : 0)}
                    afterSamples={currentUsage?.runs ?? (currentUsage ? undefined : 0)}
                    unit="%" insufficient={t(($) => $.skills.insufficientCost)}
                    noData={t(($) => $.skills.noComparisonData)} notCollected={t(($) => $.skills.notCollected)} />
                  </div>
                </div>}
              <p className="text-caption text-muted-foreground">{t(($) => $.skills.comparisonCaveat)}</p>
            </div>
            <div className="space-y-3 border-t pt-4">
              <h4 className="text-body font-medium">{t(($) => $.skills.useVsControl)}</h4>
              {effect.isPending ? <Skeleton className="h-32 w-full" /> : effect.isError || !effect.data ?
                <p role="alert" className="text-body text-destructive">{t(($) => $.skills.error)} <Button size="sm" onClick={() => effect.refetch()}>{t(($) => $.skills.retry)}</Button></p> :
                <div className="min-w-0 overflow-x-auto">
                  <div className="min-w-[28rem] space-y-2 text-caption">
                    <div className="grid grid-cols-[minmax(0,1fr)_repeat(2,minmax(0,6rem))_minmax(0,7rem)] gap-2 border-b pb-2 text-muted-foreground">
                      <span>{t(($) => $.skills.metric)}</span><span>{t(($) => $.skills.useGroup)}</span><span>{t(($) => $.skills.controlGroup)}</span><span>{t(($) => $.skills.change)}</span>
                    </div>
                    <SkillComparisonRow label={t(($) => $.skills.tokenMetric)}
                      before={effect.data.use_group.median_total_tokens} after={effect.data.control_group.median_total_tokens}
                      beforeSamples={effect.data.use_group.token_samples} afterSamples={effect.data.control_group.token_samples}
                      unit="token" insufficient={t(($) => $.skills.insufficientCost)}
                      noData={t(($) => $.skills.noCostData)} notCollected={t(($) => $.skills.notCollected)} />
                    <SkillComparisonRow label={t(($) => $.skills.retryMetric)}
                      before={effect.data.use_group.runs ? effect.data.use_group.retried_runs / effect.data.use_group.runs * 100 : null}
                      after={effect.data.control_group.runs ? effect.data.control_group.retried_runs / effect.data.control_group.runs * 100 : null}
                      beforeSamples={effect.data.use_group.runs} afterSamples={effect.data.control_group.runs}
                      unit="%" insufficient={t(($) => $.skills.insufficientCost)}
                      noData={t(($) => $.skills.noComparisonData)} notCollected={t(($) => $.skills.notCollected)} />
                    <SkillComparisonRow label={t(($) => $.skills.firstPassMetric)}
                      before={effect.data.use_group.reviewed_issues ? effect.data.use_group.first_pass_issues / effect.data.use_group.reviewed_issues * 100 : null}
                      after={effect.data.control_group.reviewed_issues ? effect.data.control_group.first_pass_issues / effect.data.control_group.reviewed_issues * 100 : null}
                      beforeSamples={effect.data.use_group.reviewed_issues} afterSamples={effect.data.control_group.reviewed_issues}
                      unit="%" insufficient={t(($) => $.skills.insufficientCost)}
                      noData={t(($) => $.skills.noReviewData)} notCollected={t(($) => $.skills.notCollected)} />
                    <div className="grid grid-cols-[minmax(0,1fr)_repeat(2,minmax(0,6rem))_minmax(0,7rem)] gap-2 break-words">
                      <span>{t(($) => $.skills.perplexityMetric)}</span>
                      <span className="col-span-2">{effect.data.perplexity.scored ? effect.data.perplexity.bands?.join(" / ") : t(($) => $.skills.perplexityNotScored)}</span>
                      <span className="text-muted-foreground">{t(($) => $.skills.notComparable)}</span>
                    </div>
                  </div>
                </div>}
              <p className="text-caption text-muted-foreground">{t(($) => $.skills.useControlCaveat)}</p>
              <p className="text-caption text-muted-foreground">{t(($) => $.skills.perplexitySamePrompt)}</p>
            </div>
            <h4 className="text-body font-medium">{t(($) => $.skills.versionEvents)}</h4>
            {effect.isPending ? <Skeleton className="h-20 w-full" /> : !effect.data?.version_events.length ?
              <p className="text-body text-muted-foreground">{t(($) => $.skills.noVersionEvents)}</p> :
              <div className="divide-y border-y">{effect.data.version_events.map((event) => (
                <div key={event.version} className="space-y-1 py-2 text-caption">
                  <p>{t(($) => $.skills.versionEventTitle, { from: event.from_version, version: event.version })}
                    <span className="text-muted-foreground"> · {sourceLabel(event.source)} · {new Date(event.created_at).toLocaleDateString(locale)}</span></p>
                  <p className="text-muted-foreground">{t(($) => $.skills.eventBefore, { count: event.before?.runs ?? 0 })}
                    {event.before?.median_total_tokens != null ? ` · ${t(($) => $.skills.medianTokens, { count: event.before.median_total_tokens })}` : ""}
                    {` · ${windowLabel(event.before_mode, event.window_days, event.window_uses)}`}</p>
                  <p className="text-muted-foreground">{t(($) => $.skills.eventAfter, { count: event.after?.runs ?? 0 })}
                    {event.after?.median_total_tokens != null ? ` · ${t(($) => $.skills.medianTokens, { count: event.after.median_total_tokens })}` : ""}
                    {` · ${windowLabel(event.after_mode, event.window_days, event.window_uses)}`}</p>
                </div>
              ))}</div>}
            <h4 className="text-body font-medium">{t(($) => $.skills.recent)}</h4>
            {!usage.data?.recent.length ? <p className="text-body text-muted-foreground">{t(($) => $.skills.noObserved)}</p> :
              <div className="divide-y border-y">{usage.data.recent.slice(0, 5).map((item, index) => (
                <div key={`${item.task_id}:${item.used_at}:${index}`} className="flex min-w-0 items-center justify-between gap-2 py-2 text-caption">
                  <span>{t(($) => $.skills.usedAt, { date: new Date(item.used_at).toLocaleString(locale), version: item.version })}</span>
                  {item.issue_id ? <AppLink href={paths.issueDetail(item.issue_id)} className="truncate text-primary">{item.issue_id}</AppLink> : <span className="text-muted-foreground">{t(($) => $.skills.chatRun)}</span>}
                </div>
              ))}</div>}
            <p className="text-caption text-muted-foreground">{t(($) => $.skills.observational)}</p>
          </>}
        </section>
      </div>

      <Sheet open={selectedVersion !== null} onOpenChange={(open) => { if (!open) setSelectedVersion(null); }}>
        <SheetContent className="w-full gap-0 overflow-y-auto sm:max-w-lg">
          <SheetHeader><SheetTitle>{t(($) => $.skills.version, { version: selectedVersion ?? 0 })}</SheetTitle><SheetDescription>{detail.data?.created_at ? new Date(detail.data.created_at).toLocaleString(locale) : ""}</SheetDescription></SheetHeader>
          {detail.isPending ? <Skeleton className="h-40 w-full" /> : detail.isError ? <p role="alert" className="text-destructive">{t(($) => $.skills.error)}</p> : detail.data?.id ? <div className="space-y-5 px-4 py-4 text-body">
            <p>{sourceLabel(detail.data.source)} · {detail.data.name}</p>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words border bg-muted/30 p-3 font-mono text-caption">{detail.data.content}</pre>
            {detail.data.files.map((file) => <div key={file.path}><p className="font-mono text-caption">{file.path}</p><pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words border p-3 font-mono text-caption">{file.content}</pre></div>)}
            {selectedVersion !== null && selectedVersion > 1 && previous.data?.id ? <div><h4 className="mb-2 font-medium">{t(($) => $.skills.diff)}</h4><PromptDiffView current={skillSnapshotText(previous.data)} incoming={skillSnapshotText(detail.data)} /></div> : null}
            {detail.data.can_restore === true ? <Button size="sm" variant="outline" onClick={() => setConfirmRestore(true)} disabled={restore.isPending}><RotateCcw className="mr-2 size-4" />{t(($) => $.skills.restore)}</Button> :
              <Tooltip><TooltipTrigger render={<span className="inline-block"><Button size="sm" disabled><RotateCcw className="mr-2 size-4" />{t(($) => $.skills.restore)}</Button></span>} /><TooltipContent>{t(($) => $.skills.ownerOnly)}</TooltipContent></Tooltip>}
            {restore.isError && <p role="alert" className="text-destructive">{t(($) => $.skills.restoreError)}</p>}
          </div> : null}
        </SheetContent>
      </Sheet>
      <AlertDialog open={confirmRestore} onOpenChange={setConfirmRestore}>
        <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t(($) => $.skills.restore)}</AlertDialogTitle><AlertDialogDescription>{t(($) => $.skills.restoreConfirm, { version: selectedVersion ?? 0, next: (versions.data?.[0]?.version ?? 0) + 1 })}</AlertDialogDescription></AlertDialogHeader>
          <AlertDialogFooter><AlertDialogCancel>{t(($) => $.skills.cancel)}</AlertDialogCancel><AlertDialogAction onClick={() => { if (selectedVersion !== null) restore.mutate(selectedVersion, { onSuccess: () => setSelectedVersion(null) }); }}>{t(($) => $.skills.restore)}</AlertDialogAction></AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function SkillComparisonRow({ label, before, after, beforeSamples, afterSamples, unit, insufficient, noData, notCollected }: {
  label: string; before?: number | null; after?: number | null;
  beforeSamples?: number; afterSamples?: number; unit: string;
  insufficient: string; noData: string; notCollected: string;
}) {
  const value = (measurement: number | null | undefined, samples: number | undefined) => {
    if (samples === undefined) return notCollected;
    if (samples === 0 || measurement == null) return noData;
    return `${Math.round(measurement).toLocaleString()} ${unit}`;
  };
  const comparable = beforeSamples !== undefined && beforeSamples >= 5
    && afterSamples !== undefined && afterSamples >= 5 && before != null && after != null;
  const delta = comparable ? `${after - before > 0 ? "+" : ""}${Math.round(after - before).toLocaleString()} ${unit}` :
    beforeSamples === undefined || afterSamples === undefined ? notCollected :
      beforeSamples === 0 || afterSamples === 0 ? noData : insufficient;
  return <div className="grid grid-cols-[minmax(0,1fr)_repeat(2,minmax(0,6rem))_minmax(0,7rem)] gap-2 break-words">
    <span>{label}</span><span>{value(before, beforeSamples)}</span><span>{value(after, afterSamples)}</span>
    <span className="text-muted-foreground">{delta}</span>
  </div>;
}

function skillSnapshotText(version: { content: string; files: { path: string; content: string }[] }) {
  return [version.content, ...version.files.map((file) => `${file.path}\n${file.content}`)].join("\n\n");
}
