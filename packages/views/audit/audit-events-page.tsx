"use client";

import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { auditEventsPageOptions } from "@multica/core/audit";
import { ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { paths, useCurrentWorkspace } from "@multica/core/paths";
import type { AuditEvent, ListAuditEventsParams } from "@multica/core/types/audit";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@multica/ui/components/ui/sheet";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { ArrowLeft, CircleAlert, ListFilter, RefreshCw, ServerOff, ShieldAlert } from "lucide-react";
import { useT } from "../i18n";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";

const RESULTS = ["", "success", "failure", "refused"] as const;
const TARGETS = ["", "workspace", "issue", "host"] as const;

type AuditDraft = {
  since: string;
  until: string;
  actorId: string;
  clientName: string;
  clientVersion: string;
  toolName: string;
  resultStatus: (typeof RESULTS)[number];
  targetType: (typeof TARGETS)[number];
  targetId: string;
};

const EMPTY_DRAFT: AuditDraft = {
  since: "", until: "", actorId: "", clientName: "", clientVersion: "", toolName: "", resultStatus: "", targetType: "", targetId: "",
};

function dateTime(value: string | null): string {
  if (value === null) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function detailString(details: Record<string, unknown>, key: string): string | null {
  const value = details[key];
  return typeof value === "string" || typeof value === "number" ? String(value) : null;
}

function eventTool(event: AuditEvent): string {
  return detailString(event.details, "tool_name") ?? event.event_type;
}

function eventResult(event: AuditEvent): string | null {
  return detailString(event.details, "result_status") ?? detailString(event.details, "status") ?? event.reason;
}

function eventDuration(event: AuditEvent): string | null {
  const duration = detailString(event.details, "duration_ms") ?? detailString(event.details, "latency_ms");
  return duration === null ? null : `${duration} ms`;
}

function eventTarget(event: AuditEvent): string | null {
  const type = detailString(event.details, "target_type");
  const id = detailString(event.details, "target_id");
  return type && id ? `${type}: ${id}` : id;
}

function filtersFromDraft(draft: AuditDraft): ListAuditEventsParams {
  return {
    domain: "mcp",
    limit: 50,
    time_basis: "invocation_start",
    ...(draft.since.trim() ? { since: draft.since.trim() } : {}),
    ...(draft.until.trim() ? { until: draft.until.trim() } : {}),
    ...(draft.actorId.trim() ? { actor_id: draft.actorId.trim() } : {}),
    ...(draft.clientName.trim() ? { client_name: draft.clientName.trim() } : {}),
    ...(draft.clientVersion.trim() ? { client_version: draft.clientVersion.trim() } : {}),
    ...(draft.toolName.trim() ? { tool_name: draft.toolName.trim() } : {}),
    ...(draft.resultStatus ? { result_status: draft.resultStatus } : {}),
    ...(draft.targetType && draft.targetId.trim()
      ? { target_type: draft.targetType as NonNullable<ListAuditEventsParams["target_type"]>, target_id: draft.targetId.trim() }
      : {}),
  };
}

function isPermissionError(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 401 || error.status === 403);
}

function isServiceUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status >= 500;
}

export function AuditEventsPage() {
  const { t } = useT("layout");
  const workspace = useCurrentWorkspace();
  const workspaceId = useWorkspaceId();
  const navigation = useNavigation();
  const [draft, setDraft] = useState<AuditDraft>(EMPTY_DRAFT);
  const [filters, setFilters] = useState<ListAuditEventsParams>(() => filtersFromDraft(EMPTY_DRAFT));
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);
  const query = useInfiniteQuery(auditEventsPageOptions(workspaceId, filters));
  const events = useMemo(() => {
    const byId = new Map<string, AuditEvent>();
    for (const page of query.data?.pages ?? []) for (const event of page.events) byId.set(event.id, event);
    return [...byId.values()];
  }, [query.data]);
  const hasFilters = Object.keys(filters).some((key) => key !== "limit" && key !== "time_basis");
  const resultOptions = RESULTS.map((value) => ({
    value,
    label: value === "success" ? t(($) => $.audit_view.result_success)
      : value === "failure" ? t(($) => $.audit_view.result_failure)
        : value === "refused" ? t(($) => $.audit_view.result_refused)
          : t(($) => $.audit_view.all_results),
  }));
  const targetOptions = TARGETS.map((value) => ({
    value,
    label: value === "workspace" ? t(($) => $.audit_view.target_workspace)
      : value === "issue" ? t(($) => $.audit_view.target_issue)
        : value === "host" ? t(($) => $.audit_view.target_host)
          : t(($) => $.audit_view.all_targets),
  }));
  const errorState = query.error ? isPermissionError(query.error) ? "permission" : isServiceUnavailable(query.error) ? "unavailable" : "failed" : null;

  function applyFilters() {
    setSelectedEvent(null);
    setFilters(filtersFromDraft(draft));
  }

  function clearFilters() {
    setDraft(EMPTY_DRAFT);
    setSelectedEvent(null);
    setFilters(filtersFromDraft(EMPTY_DRAFT));
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <div className="flex min-w-0 items-center gap-2">
          <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.audit_view.back_to_mcp)} onClick={() => navigation.push(`${paths.workspace(workspace?.slug ?? "").settings()}?tab=mcp`)}><ArrowLeft className="size-4" /></Button>
          <div className="min-w-0"><h1 className="truncate text-title font-semibold">{t(($) => $.audit_view.title)}</h1><p className="truncate text-caption text-muted-foreground">{t(($) => $.audit_view.description)}</p></div>
        </div>
      </PageHeader>
      <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4 sm:p-6">
        <section aria-label={t(($) => $.audit_view.filters)} className="rounded-lg border border-surface-border p-3">
          <div className="mb-3 flex items-center gap-2 text-body font-medium"><ListFilter className="size-4" />{t(($) => $.audit_view.filters)}</div>
          <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            <Input aria-label={t(($) => $.audit_view.since)} placeholder={t(($) => $.audit_view.since_placeholder)} value={draft.since} onChange={(event) => setDraft((current) => ({ ...current, since: event.target.value }))} />
            <Input aria-label={t(($) => $.audit_view.until)} placeholder={t(($) => $.audit_view.until_placeholder)} value={draft.until} onChange={(event) => setDraft((current) => ({ ...current, until: event.target.value }))} />
            <Input aria-label={t(($) => $.audit_view.actor_id)} placeholder={t(($) => $.audit_view.actor_id)} value={draft.actorId} onChange={(event) => setDraft((current) => ({ ...current, actorId: event.target.value }))} />
            <Input aria-label={t(($) => $.audit_view.client_name)} placeholder={t(($) => $.audit_view.client_name)} value={draft.clientName} onChange={(event) => setDraft((current) => ({ ...current, clientName: event.target.value }))} />
            <Input aria-label={t(($) => $.audit_view.client_version)} placeholder={t(($) => $.audit_view.client_version)} value={draft.clientVersion} onChange={(event) => setDraft((current) => ({ ...current, clientVersion: event.target.value }))} />
            <Input aria-label={t(($) => $.audit_view.tool)} placeholder={t(($) => $.audit_view.tool)} value={draft.toolName} onChange={(event) => setDraft((current) => ({ ...current, toolName: event.target.value }))} />
            <Select items={resultOptions} value={draft.resultStatus} onValueChange={(value) => setDraft((current) => ({ ...current, resultStatus: value as AuditDraft["resultStatus"] }))}><SelectTrigger className="w-full" aria-label={t(($) => $.audit_view.result)}><SelectValue /></SelectTrigger><SelectContent>{resultOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent></Select>
            <Select items={targetOptions} value={draft.targetType} onValueChange={(value) => setDraft((current) => ({ ...current, targetType: value as AuditDraft["targetType"] }))}><SelectTrigger className="w-full" aria-label={t(($) => $.audit_view.target_type)}><SelectValue /></SelectTrigger><SelectContent>{targetOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent></Select>
            <Input aria-label={t(($) => $.audit_view.target_id)} placeholder={t(($) => $.audit_view.target_id)} value={draft.targetId} disabled={!draft.targetType} onChange={(event) => setDraft((current) => ({ ...current, targetId: event.target.value }))} />
          </div>
          <div className="mt-3 flex flex-wrap gap-2"><Button type="button" size="sm" onClick={applyFilters}>{t(($) => $.audit_view.apply)}</Button><Button type="button" size="sm" variant="ghost" disabled={!hasFilters && !Object.values(draft).some(Boolean)} onClick={clearFilters}>{t(($) => $.audit_view.clear)}</Button></div>
        </section>
        {query.isLoading ? <AuditLoading /> : null}
        {errorState ? <AuditErrorState state={errorState} retry={() => void query.refetch()} /> : null}
        {!query.isLoading && !errorState && events.length === 0 ? <EmptyState filtered={hasFilters} clearFilters={clearFilters} /> : null}
        {!query.isLoading && !errorState && events.length > 0 ? <AuditEventList events={events} selectedId={selectedEvent?.id ?? null} onSelect={setSelectedEvent} /> : null}
        {!errorState && query.hasNextPage ? <div className="flex justify-center pb-2"><Button type="button" variant="outline" onClick={() => void query.fetchNextPage()} disabled={query.isFetchingNextPage}>{query.isFetchingNextPage ? <RefreshCw className="size-4 animate-spin" /> : null}{query.isFetchingNextPage ? t(($) => $.audit_view.loading_more) : t(($) => $.audit_view.load_more)}</Button></div> : null}
      </div>
      <AuditEventDetail event={selectedEvent} onOpenChange={(open) => { if (!open) setSelectedEvent(null); }} />
    </div>
  );
}

function AuditLoading() { return <div className="space-y-2">{Array.from({ length: 5 }, (_, index) => <Skeleton key={index} className="h-12 w-full" />)}</div>; }

function AuditErrorState({ state, retry }: { state: "permission" | "unavailable" | "failed"; retry: () => void }) {
  const { t } = useT("layout");
  const content = state === "permission" ? { icon: ShieldAlert, title: t(($) => $.audit_view.permission_title), description: t(($) => $.audit_view.permission_description) } : state === "unavailable" ? { icon: ServerOff, title: t(($) => $.audit_view.unavailable_title), description: t(($) => $.audit_view.unavailable_description) } : { icon: CircleAlert, title: t(($) => $.audit_view.load_failed_title), description: t(($) => $.audit_view.load_failed) };
  const Icon = content.icon;
  return <Alert variant={state === "permission" ? "default" : "destructive"}><Icon /><AlertTitle>{content.title}</AlertTitle><AlertDescription>{content.description}</AlertDescription><Button size="sm" variant="outline" onClick={retry}>{t(($) => $.audit_view.retry)}</Button></Alert>;
}

function EmptyState({ filtered, clearFilters }: { filtered: boolean; clearFilters: () => void }) {
  const { t } = useT("layout");
  return <div className="py-12 text-center"><p className="text-body text-muted-foreground">{filtered ? t(($) => $.audit_view.empty_filtered) : t(($) => $.audit_view.empty)}</p>{filtered ? <Button className="mt-3" size="sm" variant="outline" onClick={clearFilters}>{t(($) => $.audit_view.clear)}</Button> : null}</div>;
}

function AuditEventList({ events, selectedId, onSelect }: { events: AuditEvent[]; selectedId: string | null; onSelect: (event: AuditEvent) => void }) {
  const { t } = useT("layout");
  return <><div className="space-y-2 md:hidden">{events.map((event) => <button key={event.id} type="button" onClick={() => onSelect(event)} className="w-full rounded-lg border border-surface-border p-3 text-left hover:bg-surface-hover"><p className="truncate text-body font-medium">{eventTool(event)}</p><p className="mt-1 text-caption text-muted-foreground">{dateTime(event.occurred_at)} · {event.actor_id ?? event.actor_type}</p><p className="mt-1 truncate text-caption text-muted-foreground">{eventTarget(event) ?? "-"}</p></button>)}</div><div className="hidden overflow-x-auto rounded-lg border border-surface-border md:block"><table className="w-full min-w-[760px] text-left text-caption"><thead className="bg-muted/40 text-muted-foreground"><tr><th className="p-3">{t(($) => $.audit_view.time)}</th><th className="p-3">{t(($) => $.audit_view.tool)}</th><th className="p-3">{t(($) => $.audit_view.result)}</th><th className="p-3">{t(($) => $.audit_view.actor)}</th><th className="p-3">{t(($) => $.audit_view.target)}</th></tr></thead><tbody>{events.map((event) => <tr key={event.id} role="button" tabIndex={0} data-state={selectedId === event.id ? "selected" : undefined} onClick={() => onSelect(event)} onKeyDown={(keyboardEvent) => { if (keyboardEvent.key === "Enter" || keyboardEvent.key === " ") { keyboardEvent.preventDefault(); onSelect(event); } }} className="cursor-pointer border-t align-top hover:bg-surface-hover data-[state=selected]:bg-surface-selected"><td className="p-3 whitespace-nowrap">{dateTime(event.occurred_at)}</td><td className="max-w-48 truncate p-3 font-medium">{eventTool(event)}</td><td className="p-3">{eventResult(event) ?? t(($) => $.audit_view.started)}</td><td className="max-w-48 truncate p-3">{event.actor_id ?? event.actor_type}</td><td className="max-w-64 truncate p-3">{eventTarget(event) ?? "-"}</td></tr>)}</tbody></table></div></>;
}

function AuditEventDetail({ event, onOpenChange }: { event: AuditEvent | null; onOpenChange: (open: boolean) => void }) {
  const { t } = useT("layout");
  return <Sheet open={event !== null} onOpenChange={onOpenChange}><SheetContent side="right" className="w-full gap-5 overflow-y-auto p-4 sm:max-w-md">{event ? <><SheetHeader className="p-0"><SheetTitle>{eventTool(event)}</SheetTitle></SheetHeader><div className="space-y-4"><DetailRow label={t(($) => $.audit_view.time)} value={dateTime(event.occurred_at)} /><DetailRow label={t(($) => $.audit_view.event_type)} value={event.event_type} /><DetailRow label={t(($) => $.audit_view.actor)} value={event.actor_id ?? event.actor_type} /><DetailRow label={t(($) => $.audit_view.client)} value={[detailString(event.details, "reported_client_name"), detailString(event.details, "reported_client_version")].filter(Boolean).join(" ") || null} /><DetailRow label={t(($) => $.audit_view.tool)} value={detailString(event.details, "tool_name")} /><DetailRow label={t(($) => $.audit_view.target)} value={eventTarget(event)} /><DetailRow label={t(($) => $.audit_view.result)} value={eventResult(event)} /><DetailRow label={t(($) => $.audit_view.duration)} value={eventDuration(event)} /><DetailRow label={t(($) => $.audit_view.error_code)} value={detailString(event.details, "error_code")} /><DetailRow label={t(($) => $.audit_view.invocation_id)} value={event.invocation_id ?? null} /></div></> : null}</SheetContent></Sheet>;
}

function DetailRow({ label, value }: { label: string; value: string | null }) {
  const { t } = useT("layout");
  return <div className="space-y-1"><p className="text-caption font-medium text-muted-foreground">{label}</p><p className="break-words text-body">{value ?? t(($) => $.audit_view.not_provided)}</p></div>;
}
