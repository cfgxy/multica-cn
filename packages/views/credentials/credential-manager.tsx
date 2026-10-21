"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, KeyRound, LoaderCircle, Plus, RotateCw, ShieldCheck, X } from "lucide-react";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { encryptCredentialForTransport, CREDENTIAL_ENVELOPE_VERSION } from "@multica/core/credentials/envelope";
import type { CredentialMetadata } from "@multica/core/credentials/api";
import { issueListOptions } from "@multica/core/issues/queries";
import { issueStatusCategory } from "@multica/core/issues";
import { memberListOptions, agentListOptions, squadListOptions } from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import type { Issue } from "@multica/core/types";
import { useT } from "../i18n";
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";

type CredentialTarget = Pick<Issue, "id" | "identifier" | "title" | "project_id" | "status" | "status_category" | "assignee_type" | "assignee_id">;
type Scope = "single_use" | "issue" | "project" | "workspace" | "global";

const terminalStatuses = new Set(["done", "cancelled"]);

function displayStatus(item: CredentialMetadata, issueStatus: string): string {
  if (item.status === "revoked" || item.status === "exhausted" || item.status === "expired") return item.status;
  if (item.expires_at && Date.parse(item.expires_at) <= Date.now()) return "expired";
  if (item.resource_scope === "issue" && terminalStatuses.has(issueStatus)) return "expired";
  return item.status === "active" ? "active" : item.status;
}

export function CredentialManager({ issue, workspaceMode = false }: { issue?: CredentialTarget; workspaceMode?: boolean }) {
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const { t } = useT("settings");
  const scopeTitles: Record<Scope, string> = {
    single_use: t(($) => $.credential_manager.scope_once),
    issue: t(($) => $.credential_manager.scope_task),
    project: t(($) => $.credential_manager.scope_project),
    workspace: t(($) => $.credential_manager.scope_workspace),
    global: t(($) => $.credential_manager.scope_global),
  };
  const currentUser = useAuthStore((state) => state.user);
  const workspaceIssues = useQuery({ ...issueListOptions(wsId), enabled: workspaceMode && !!wsId });
  const targets = useMemo(() => workspaceMode ? workspaceIssues.data ?? [] : issue ? [issue] : [], [issue, workspaceIssues.data, workspaceMode]);
  const [selectedIssueId, setSelectedIssueId] = useState(issue?.id ?? "");
  const queryTargets = workspaceMode
    ? targets.filter((target) => target.id === selectedIssueId)
    : issue ? [issue] : [];
  const credentialQueries = useQueries({
    queries: queryTargets.map((target) => ({
      queryKey: ["credentials", wsId, "issue", target.id],
      queryFn: () => api.listIssueCredentials(target.id),
      enabled: !!wsId,
    })),
  });
  const { data: connectors = [] } = useQuery({
    queryKey: ["credentials", wsId, "connectors"],
    queryFn: () => api.listCredentialConnectors(wsId),
    enabled: !!wsId,
    retry: false,
  });
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const currentMember = members.find((member) => member.user_id === currentUser?.id);
  const isOwner = currentMember?.role === "owner";
  const { data: credentialAudit = [], isLoading: isAuditLoading } = useQuery({
    queryKey: ["credentials", wsId, "audit"],
    queryFn: () => api.listCredentialAudit(wsId),
    enabled: workspaceMode && !!wsId && isOwner,
    retry: false,
  });
  const canWriteIssue = isOwner || (!!issue && issue.assignee_type === "member" && issue.assignee_id === currentUser?.id);
  const [formOpen, setFormOpen] = useState(false);
  const [name, setName] = useState("");
  const [purpose, setPurpose] = useState("");
  const [secret, setSecret] = useState("");
  const secretForSubmission = useRef("");
  const [connectorId, setConnectorId] = useState("");
  const [subject, setSubject] = useState("");
  const [scope, setScope] = useState<Scope>("issue");
  const [expiresAt, setExpiresAt] = useState("");
  const [replacing, setReplacing] = useState<{ issueId: string; item: CredentialMetadata } | null>(null);
  const [message, setMessage] = useState("");

  useEffect(() => () => { secretForSubmission.current = ""; }, []);

  const selectedConnector = connectors.find((connector) => connector.id === connectorId && connector.status === "active");
  const subjectParts = subject.split(":");
  const subjectType: "agent" | "squad" = subjectParts[0] === "squad" ? "squad" : "agent";
  const subjectId = subjectParts[1] ?? "";
  const broadScopeDenied = !isOwner && scope !== "issue" && scope !== "single_use";
  const selectedTarget = replacing ? targets.find((target) => target.id === replacing.issueId) : issue;
  const createMutation = useMutation({
    mutationFn: async () => {
      if (!selectedTarget || !selectedConnector || !subjectId) throw new Error("invalid");
      const key = await api.getCredentialTransportKey();
      if (!key) throw new Error("unavailable");
      const value = secretForSubmission.current;
      secretForSubmission.current = "";
      if (!value) throw new Error("invalid");
      const resourceId = scope === "issue" || scope === "single_use"
        ? selectedTarget.id
        : scope === "project"
          ? selectedTarget.project_id ?? ""
          : scope === "workspace" ? wsId : "global";
      const envelope = await encryptCredentialForTransport(value, key, {
        workspace_id: wsId,
        resource_type: scope,
        resource_id: resourceId,
        subject_type: subjectType,
        subject_id: subjectId,
        purpose: "credential.create",
        version: CREDENTIAL_ENVELOPE_VERSION,
      });
      const request = {
        name: name.trim(),
        target_service: selectedConnector.name,
        connector_id: selectedConnector.id,
        connector_revision: selectedConnector.revision,
        purpose: purpose.trim(),
        subject_type: subjectType,
        subject_id: subjectId,
        resource_scope: scope,
        resource_id: resourceId,
        expires_at: scope === "project" || scope === "workspace" || scope === "global" ? new Date(expiresAt).toISOString() : null,
        envelope,
      };
      return replacing
        ? api.replaceIssueCredential(replacing.issueId, replacing.item.id, request)
        : api.createIssueCredential(selectedTarget.id, request);
    },
    onSuccess: async (created) => {
      if (!created || !selectedTarget) {
          setMessage(t(($) => $.credential_manager.save_failed));
        return;
      }
      await queryClient.invalidateQueries({ queryKey: ["credentials", wsId] });
      setMessage(replacing ? t(($) => $.credential_manager.replaced) : t(($) => $.credential_manager.saved));
      setReplacing(null);
      clearForm();
      await queryClient.invalidateQueries({ queryKey: ["credentials", wsId] });
    },
    onError: () => setMessage(t(($) => $.credential_manager.save_failed)),
    onSettled: () => { secretForSubmission.current = ""; setSecret(""); },
  });

  const revokeMutation = useMutation({
    mutationFn: ({ issueId, credentialId }: { issueId: string; credentialId: string }) => api.revokeIssueCredential(issueId, credentialId),
    onSuccess: async () => {
      setMessage(t(($) => $.credential_manager.revoked));
      await queryClient.invalidateQueries({ queryKey: ["credentials", wsId] });
    },
    onError: () => setMessage(t(($) => $.credential_manager.revoke_failed)),
  });

  function clearForm() {
    setSecret("");
    setName("");
    setPurpose("");
    setConnectorId("");
    setSubject("");
    setScope("issue");
    setExpiresAt("");
    setFormOpen(false);
  }

  function beginReplace(target: CredentialTarget, item: CredentialMetadata) {
    setReplacing({ issueId: target.id, item });
    setName(item.name);
    setPurpose(item.purpose);
    setScope(item.resource_scope as Scope);
    setConnectorId(item.connector_id);
    setSubject(`${item.subject_type}:${item.subject_id}`);
    setExpiresAt(item.expires_at ? new Date(item.expires_at).toISOString().slice(0, 16) : "");
    setFormOpen(true);
    setMessage("");
  }

  const allCredentials = credentialQueries.flatMap((query, index) =>
    (query.data ?? []).map((item) => ({ item, target: queryTargets[index] })),
  );
  const activeSubjects = [
    ...agents.filter((agent) => !agent.archived_at).map((agent) => ({ id: `agent:${agent.id}`, label: agent.name, type: "agent" as const })),
    ...squads.map((squad) => ({ id: `squad:${squad.id}`, label: squad.name, type: "squad" as const })),
  ];
  const selectedSubject = activeSubjects.find((item) => item.id === subject);

  return (
    <section className="min-w-0 space-y-3" aria-labelledby="credential-manager-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 id="credential-manager-title" className="flex items-center gap-2 text-body font-semibold">
          <KeyRound className="size-4" /> {t(($) => $.page.tabs.credentials)}
        </h3>
        {!workspaceMode && issue && canWriteIssue ? (
          <Button size="sm" variant="outline" onClick={() => { setReplacing(null); setMessage(""); setFormOpen((open) => !open); if (formOpen) clearForm(); }}>
            <Plus className="size-4" /> {formOpen ? t(($) => $.credential_manager.close) : t(($) => $.credential_manager.deliver)}
          </Button>
        ) : null}
      </div>

      <p className="text-caption leading-5 text-muted-foreground">{t(($) => $.credential_manager.trust_boundary)}</p>

      {workspaceMode && isOwner ? (
        <section className="space-y-2" aria-labelledby="credential-audit-title">
          <h4 id="credential-audit-title" className="text-body font-semibold">{t(($) => $.credential_manager.audit_title)}</h4>
          {isAuditLoading ? <p className="text-caption text-muted-foreground">{t(($) => $.credential_manager.loading)}</p> : null}
          {!isAuditLoading && credentialAudit.length === 0 ? <p className="text-caption text-muted-foreground">{t(($) => $.credential_manager.audit_empty)}</p> : null}
          <ul className="space-y-2">
            {credentialAudit.map((event) => (
              <li key={event.id} className="min-w-0 border-b border-surface-border py-2 last:border-0">
                <p className="break-words text-caption font-medium">{new Date(event.created_at).toLocaleString()} · {event.event_type} · {event.result} ({event.reason_code})</p>
                <p className="break-words text-caption text-muted-foreground">
                  {t(($) => $.credential_manager.audit_project)}: {event.project_title ?? event.project_id ?? "—"} · {t(($) => $.credential_manager.audit_issue)}: {event.issue_title ?? event.issue_id ?? "—"}
                </p>
                <p className="break-words text-caption text-muted-foreground">
                  {t(($) => $.credential_manager.audit_actor)}: {event.actor_type} / {event.actor_id} · {t(($) => $.credential_manager.purpose)}: {event.purpose || "—"}
                </p>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {message ? <Alert role="status"><AlertDescription>{message}</AlertDescription></Alert> : null}

      {formOpen && selectedTarget ? (
        <form className="space-y-4 rounded-md border border-surface-border p-3 sm:p-4" onSubmit={(event) => { event.preventDefault(); if (createMutation.isPending) return; secretForSubmission.current = secret; setSecret(""); setMessage(""); createMutation.mutate(); }}>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="credential-name">{t(($) => $.credential_manager.name)}</Label>
              <Input id="credential-name" value={name} maxLength={200} required onChange={(event) => setName(event.target.value)} autoComplete="off" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="credential-connector">{t(($) => $.credential_manager.connector)}</Label>
              <Select items={connectors.filter((item) => item.status === "active").map((item) => ({ value: item.id, label: item.name }))} value={connectorId} onValueChange={(value) => setConnectorId(value ?? "")}>
                <SelectTrigger id="credential-connector" aria-label={t(($) => $.credential_manager.connector)}><SelectValue placeholder={t(($) => $.credential_manager.select_connector)} /></SelectTrigger>
                <SelectContent>{connectors.filter((item) => item.status === "active").map((item) => <SelectItem key={item.id} value={item.id}>{item.name}</SelectItem>)}</SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="credential-purpose">{t(($) => $.credential_manager.purpose)}</Label>
            <Textarea id="credential-purpose" value={purpose} maxLength={2000} required onChange={(event) => setPurpose(event.target.value)} rows={2} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="credential-secret">{t(($) => $.credential_manager.secret)}</Label>
            <Input id="credential-secret" type="password" value={secret} required autoComplete="new-password" spellCheck={false} onChange={(event) => setSecret(event.target.value)} className="font-mono" aria-describedby="credential-secret-help" />
            <p id="credential-secret-help" className="text-caption text-muted-foreground">{t(($) => $.credential_manager.secret_help)}</p>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="credential-scope">{t(($) => $.credential_manager.resource)}</Label>
              <Select items={(Object.keys(scopeTitles) as Scope[]).map((value) => ({ value, label: scopeTitles[value] }))} value={scope} onValueChange={(value) => { if (value) setScope(value as Scope); }}>
                <SelectTrigger id="credential-scope"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {(Object.keys(scopeTitles) as Scope[]).map((value) => <SelectItem key={value} value={value} disabled={!isOwner && value !== "issue" && value !== "single_use" || value === "project" && !selectedTarget.project_id}>{scopeTitles[value]}{value === "issue" ? ` (${selectedTarget.identifier})` : ""}</SelectItem>)}
                </SelectContent>
              </Select>
          {!isOwner ? <p className="text-caption text-muted-foreground">{t(($) => $.credential_manager.member_scope_hint)}</p> : null}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="credential-subject">{t(($) => $.credential_manager.subject)}</Label>
              <Select items={activeSubjects.map((item) => ({ value: item.id, label: `${t(($) => $.credential_manager[item.type])}: ${item.label}` }))} value={subject} onValueChange={(value) => setSubject(value ?? "")}>
                <SelectTrigger id="credential-subject"><SelectValue placeholder={t(($) => $.credential_manager.select_subject)} /></SelectTrigger>
                <SelectContent>{activeSubjects.map((item) => <SelectItem key={item.id} value={item.id}>{t(($) => $.credential_manager[item.type])}: {item.label}</SelectItem>)}</SelectContent>
              </Select>
            </div>
          </div>
          {scope === "project" || scope === "workspace" || scope === "global" ? (
            <div className="space-y-1.5">
              <Label htmlFor="credential-expiry">{t(($) => $.credential_manager.expires)}</Label>
              <Input id="credential-expiry" type="datetime-local" value={expiresAt} required onChange={(event) => setExpiresAt(event.target.value)} />
            </div>
          ) : null}
          {broadScopeDenied ? <Alert><AlertDescription>{t(($) => $.credential_manager.owner_scope_denied)}</AlertDescription></Alert> : null}
          <div className="flex items-start gap-2 rounded-md bg-muted/40 p-3 text-caption leading-5">
            <ShieldCheck className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 space-y-1">
              <p>{t(($) => $.credential_manager.dual_match)}</p>
              <p className="break-words font-medium">
                {t(($) => $.credential_manager.summary)} {scopeTitles[scope]} · {selectedSubject ? `${t(($) => $.credential_manager[selectedSubject.type])}: ${selectedSubject.label}` : "—"}
                {expiresAt ? ` · ${t(($) => $.credential_manager.expires_prefix)} ${new Date(expiresAt).toLocaleString()}` : ""}
              </p>
            </div>
          </div>
          <div className="flex flex-wrap justify-end gap-2">
            <Button type="button" variant="ghost" onClick={() => { clearForm(); setReplacing(null); }}>{t(($) => $.credential_manager.cancel)}</Button>
            <Button type="submit" disabled={createMutation.isPending || !selectedConnector || !subjectId || broadScopeDenied || !secret.trim() || !name.trim() || !purpose.trim() || ((scope === "project" || scope === "workspace" || scope === "global") && !expiresAt)}>
              {createMutation.isPending ? <LoaderCircle className="size-4 animate-spin" /> : replacing ? <RotateCw className="size-4" /> : <ShieldCheck className="size-4" />}
              {createMutation.isPending ? t(($) => $.credential_manager.saving) : replacing ? t(($) => $.credential_manager.replace) : t(($) => $.credential_manager.save)}
            </Button>
          </div>
        </form>
      ) : null}

      {workspaceMode ? (
        <div className="space-y-1.5">
          <Label htmlFor="credential-task-filter">{t(($) => $.credential_manager.task_filter)}</Label>
          <Select
            items={targets.map((target) => ({ value: target.id, label: `${target.identifier} · ${target.title}` }))}
            value={selectedIssueId}
            onValueChange={(value) => setSelectedIssueId(value ?? "")}
          >
            <SelectTrigger id="credential-task-filter"><SelectValue placeholder={t(($) => $.credential_manager.choose_task)} /></SelectTrigger>
            <SelectContent>{targets.map((target) => <SelectItem key={target.id} value={target.id}>{target.identifier} · {target.title}</SelectItem>)}</SelectContent>
          </Select>
        </div>
      ) : null}

      {workspaceMode && workspaceIssues.isLoading ? <p className="text-caption text-muted-foreground">{t(($) => $.credential_manager.loading)}</p> : null}
      {allCredentials.length === 0 && !credentialQueries.some((query) => query.isLoading) && (!workspaceMode || !!selectedIssueId) ? (
        <p className="rounded-md border border-dashed border-surface-border px-3 py-5 text-center text-caption text-muted-foreground">{t(($) => $.credential_manager.empty)}</p>
      ) : null}
      <ul className="space-y-2">
        {allCredentials.map(({ item, target }) => {
          if (!target) return null;
          const status = displayStatus(item, issueStatusCategory(target) ?? target.status);
          const canManage = isOwner || ((item.resource_scope === "issue" || item.resource_scope === "single_use") && target.assignee_type === "member" && target.assignee_id === currentUser?.id);
          return (
            <li key={`${target.id}:${item.id}`} className="min-w-0 rounded-md border border-surface-border p-3">
              <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
                <div className="min-w-0 space-y-1">
                  <p className="break-words text-body font-medium">{item.name}</p>
                  <p className="break-words text-caption text-muted-foreground">{item.purpose}</p>
                  <p className="break-words text-caption text-muted-foreground">
                    {target.identifier} · {scopeTitles[item.resource_scope as Scope] ?? item.resource_scope} · {t(($) => $.credential_manager[item.subject_type === "squad" ? "squad" : "agent"])}: {activeSubjects.find((subjectOption) => subjectOption.id === `${item.subject_type}:${item.subject_id}`)?.label ?? item.subject_id}
                  </p>
                  <p className="text-caption">{t(($) => $.credential_manager.status)} <span className="font-medium">{status}</span>{item.expires_at ? ` · ${t(($) => $.credential_manager.expires_prefix)} ${new Date(item.expires_at).toLocaleString()}` : ""}</p>
                </div>
                {status === "active" && canManage ? (
                  <div className="flex shrink-0 gap-1">
                    <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.credential_manager.replace_aria, { name: item.name })} title={t(($) => $.credential_manager.replace_title)} onClick={() => beginReplace(target, item)}><RotateCw className="size-4" /></Button>
                    <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.credential_manager.revoke_aria, { name: item.name })} title={t(($) => $.credential_manager.revoke_title)} disabled={revokeMutation.isPending} onClick={() => {
                      if (window.confirm(t(($) => $.credential_manager.revoke_confirm))) revokeMutation.mutate({ issueId: target.id, credentialId: item.id });
                    }}><X className="size-4" /></Button>
                  </div>
                ) : null}
              </div>
            </li>
          );
        })}
      </ul>
      {!workspaceMode && !canWriteIssue ? <p className="text-caption text-muted-foreground">{t(($) => $.credential_manager.no_permission)}</p> : null}
      {connectors.length === 0 && !workspaceMode && canWriteIssue ? <p className="flex items-start gap-2 text-caption text-muted-foreground"><AlertTriangle className="mt-0.5 size-4 shrink-0" /> {t(($) => $.credential_manager.no_connector)}</p> : null}
    </section>
  );
}
