"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { auditEventsOptions } from "@multica/core/audit";
import { useWorkspaceId } from "@multica/core/hooks";
import type { ListAuditEventsParams } from "@multica/core/types/audit";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../i18n";
import { PageHeader } from "../layout/page-header";

const RESULTS = ["", "success", "failure", "refused"] as const;
const TARGETS = ["", "workspace", "issue", "host"] as const;

function dateTime(value: string | null): string {
  if (value === null) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

export function AuditEventsPage() {
  const { t } = useT("layout");
  const workspaceId = useWorkspaceId();
  const [filters, setFilters] = useState<ListAuditEventsParams>({ limit: 50 });
  const [draft, setDraft] = useState({ since: "", tool_name: "", actor_id: "", target_id: "", result_status: "", target_type: "" });
  const query = useQuery(auditEventsOptions(workspaceId, filters));

  function applyFilters() {
    setFilters({
      limit: 50,
      ...(draft.since.trim() ? { since: draft.since.trim() } : {}),
      ...(draft.tool_name.trim() ? { tool_name: draft.tool_name.trim() } : {}),
      ...(draft.actor_id.trim() ? { actor_id: draft.actor_id.trim() } : {}),
      ...(draft.target_id.trim() && draft.target_type ? { target_id: draft.target_id.trim(), target_type: draft.target_type as NonNullable<ListAuditEventsParams["target_type"]> } : {}),
      ...(draft.result_status ? { result_status: draft.result_status as NonNullable<ListAuditEventsParams["result_status"]> } : {}),
    });
  }

  function resultLabel(value: (typeof RESULTS)[number]) {
    switch (value) {
      case "success": return t(($) => $.audit_view.result_success);
      case "failure": return t(($) => $.audit_view.result_failure);
      case "refused": return t(($) => $.audit_view.result_refused);
      default: return t(($) => $.audit_view.all_results);
    }
  }

  function targetLabel(value: (typeof TARGETS)[number]) {
    switch (value) {
      case "workspace": return t(($) => $.audit_view.target_workspace);
      case "issue": return t(($) => $.audit_view.target_issue);
      case "host": return t(($) => $.audit_view.target_host);
      default: return t(($) => $.audit_view.all_targets);
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <div className="min-w-0">
          <h1 className="truncate text-title font-semibold">{t(($) => $.audit_view.title)}</h1>
          <p className="truncate text-caption text-muted-foreground">{t(($) => $.audit_view.description)}</p>
        </div>
      </PageHeader>
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4 sm:p-6">
        <div className="grid gap-2 md:grid-cols-6">
          <Input aria-label={t(($) => $.audit_view.since)} placeholder={t(($) => $.audit_view.since_placeholder)} value={draft.since} onChange={(event) => setDraft((current) => ({ ...current, since: event.target.value }))} />
          <Input aria-label={t(($) => $.audit_view.tool)} placeholder={t(($) => $.audit_view.tool)} value={draft.tool_name} onChange={(event) => setDraft((current) => ({ ...current, tool_name: event.target.value }))} />
          <Input aria-label={t(($) => $.audit_view.actor_id)} placeholder={t(($) => $.audit_view.actor_id)} value={draft.actor_id} onChange={(event) => setDraft((current) => ({ ...current, actor_id: event.target.value }))} />
          <select aria-label={t(($) => $.audit_view.result)} className="h-9 border bg-background px-2 text-body" value={draft.result_status} onChange={(event) => setDraft((current) => ({ ...current, result_status: event.target.value }))}>
            {RESULTS.map((value) => <option key={value} value={value}>{resultLabel(value)}</option>)}
          </select>
          <select aria-label={t(($) => $.audit_view.target_type)} className="h-9 border bg-background px-2 text-body" value={draft.target_type} onChange={(event) => setDraft((current) => ({ ...current, target_type: event.target.value }))}>
            {TARGETS.map((value) => <option key={value} value={value}>{targetLabel(value)}</option>)}
          </select>
          <div className="flex gap-2"><Input aria-label={t(($) => $.audit_view.target_id)} placeholder={t(($) => $.audit_view.target_id)} value={draft.target_id} onChange={(event) => setDraft((current) => ({ ...current, target_id: event.target.value }))} /><Button type="button" onClick={applyFilters}>{t(($) => $.audit_view.apply)}</Button></div>
        </div>
        {query.isLoading ? <p className="text-body text-muted-foreground">{t(($) => $.audit_view.loading)}</p> : null}
        {query.isError ? <p role="alert" className="text-body text-destructive">{t(($) => $.audit_view.load_failed)}</p> : null}
        {query.data?.events.length === 0 ? <p className="text-body text-muted-foreground">{t(($) => $.audit_view.empty)}</p> : null}
        <div className="overflow-x-auto border">
          <table className="w-full min-w-[860px] text-left text-caption">
            <thead className="bg-muted/40 text-muted-foreground"><tr><th className="p-3">{t(($) => $.audit_view.time)}</th><th className="p-3">{t(($) => $.audit_view.tool)}</th><th className="p-3">{t(($) => $.audit_view.result)}</th><th className="p-3">{t(($) => $.audit_view.actor)}</th><th className="p-3">{t(($) => $.audit_view.target)}</th><th className="p-3">{t(($) => $.audit_view.reason)}</th></tr></thead>
            <tbody>{query.data?.events.map((event) => <tr key={event.id} className="border-t align-top"><td className="p-3 whitespace-nowrap">{dateTime(event.occurred_at)}</td><td className="p-3 font-medium">{String(event.details.tool_name ?? event.event_type)}</td><td className="p-3">{String(event.details.result_status ?? event.reason ?? t(($) => $.audit_view.started))}</td><td className="p-3 break-all">{event.actor_id ?? event.actor_type}</td><td className="p-3 break-all">{String(event.details.target_id ?? event.issue_id ?? "-")}</td><td className="p-3">{event.reason ?? String(event.details.error_code ?? "-")}</td></tr>)}</tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
