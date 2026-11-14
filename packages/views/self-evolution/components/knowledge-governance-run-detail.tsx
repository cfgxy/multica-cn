"use client";

import { useQuery } from "@tanstack/react-query";
import type { AgentTask, KnowledgeGovernanceFinding, KnowledgeGovernanceRun } from "@multica/core/types";
import { knowledgeGovernanceRunOptions } from "@multica/core/self-evolution";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { TranscriptButton } from "../../common/task-transcript";
import { useLocale, useT } from "../../i18n";

type JsonRecord = Record<string, unknown>;

function asRecord(value: unknown): JsonRecord {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as JsonRecord
    : {};
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function countEntries(report: Record<string, unknown>): Array<[string, number]> {
  return Object.entries(asRecord(report.counts)).filter(
    (entry): entry is [string, number] => typeof entry[1] === "number",
  );
}

function runStatusLabel(status: string, t: ReturnType<typeof useT<"self-evolution">>["t"]): string {
  switch (status) {
    case "running": return t(($) => $.knowledgeSeg.governance.status.running);
    case "succeeded": return t(($) => $.knowledgeSeg.governance.status.succeeded);
    case "failed": return t(($) => $.knowledgeSeg.governance.status.failed);
    case "cancelled": return t(($) => $.knowledgeSeg.governance.status.cancelled);
    default: return status;
  }
}

function findingEvidence(finding: KnowledgeGovernanceFinding): JsonRecord[] {
  return finding.counter_evidence
    .filter((item): item is JsonRecord => item !== null && typeof item === "object" && !Array.isArray(item));
}

function syntheticTask(run: KnowledgeGovernanceRun): AgentTask | null {
  if (!run.task_id) return null;
  return {
    id: run.task_id,
    agent_id: run.agent_id,
    runtime_id: "",
    issue_id: "",
    status: run.status === "running" ? "running" : run.status === "succeeded" ? "completed" : run.status === "failed" ? "failed" : "cancelled",
    priority: 0,
    dispatched_at: null,
    started_at: run.created_at,
    completed_at: run.finished_at ?? null,
    result: null,
    error: stringValue(run.report.reason) || null,
    created_at: run.created_at,
  };
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-caption text-muted-foreground">{label}</span>
      <span className="break-words text-body">{children}</span>
    </div>
  );
}

export function KnowledgeGovernanceRunDetailSheet({
  wsId,
  run,
  canManage,
  pending,
  onOpenChange,
  onCancel,
  onRetry,
}: {
  wsId: string;
  run: KnowledgeGovernanceRun | null;
  canManage: boolean;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onCancel: (run: KnowledgeGovernanceRun) => void;
  onRetry: (run: KnowledgeGovernanceRun) => void;
}) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const detailQuery = useQuery(knowledgeGovernanceRunOptions(wsId, run?.id ?? ""));
  const current = detailQuery.data?.run ?? run;
  const findings = detailQuery.data?.findings ?? [];
  const task = current ? syntheticTask(current) : null;

  return (
    <Sheet open={run !== null} onOpenChange={onOpenChange}>
      <SheetContent className="w-full gap-0 overflow-y-auto sm:max-w-[540px]" data-testid="knowledge-governance-drawer">
        {current ? (
          <>
            <SheetHeader>
              <SheetTitle>{current.title}</SheetTitle>
              <SheetDescription className="font-mono text-caption">{current.id}</SheetDescription>
            </SheetHeader>
            <div className="flex flex-col gap-4 px-4 py-4">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={current.status === "succeeded" ? "secondary" : current.status === "failed" ? "destructive" : "outline"}>
                  {runStatusLabel(current.status, t)}
                </Badge>
                {task ? (
                  <TranscriptButton
                    task={task}
                    agentName={t(($) => $.knowledgeSeg.governance.agentName)}
                    isLive={current.status === "running"}
                    title={t(($) => $.knowledgeSeg.governance.viewLog)}
                  />
                ) : null}
              </div>

              <div className="grid grid-cols-1 gap-3 border-t pt-3">
                <DetailRow label={t(($) => $.knowledgeSeg.governance.promptVersion)}>{current.prompt_version}</DetailRow>
                <DetailRow label={t(($) => $.knowledgeSeg.governance.startedAt)}>{new Date(current.created_at).toLocaleString(locale)}</DetailRow>
                <DetailRow label={t(($) => $.knowledgeSeg.governance.finishedAt)}>
                  {current.finished_at ? new Date(current.finished_at).toLocaleString(locale) : t(($) => $.knowledgeSeg.governance.inProgress)}
                </DetailRow>
              </div>

              {countEntries(current.report).length > 0 ? (
                <div className="border-t pt-3">
                  <h4 className="text-body font-medium">{t(($) => $.knowledgeSeg.governance.report)}</h4>
                  <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-caption">
                    {countEntries(current.report).map(([key, count]) => (
                      <div key={key} className="flex justify-between gap-2">
                        <dt className="text-muted-foreground">{key}</dt>
                        <dd>{count}</dd>
                      </div>
                    ))}
                  </dl>
                  {stringValue(current.report.backup_reference) ? (
                    <p className="mt-3 break-words text-caption text-muted-foreground">
                      {t(($) => $.knowledgeSeg.governance.backup)}: {stringValue(current.report.backup_reference)}
                    </p>
                  ) : null}
                </div>
              ) : null}

              {detailQuery.isPending ? <div className="h-32 animate-pulse rounded-md bg-muted" /> : null}
              {findings.length > 0 ? (
                <div className="flex flex-col gap-3 border-t pt-3">
                  <h4 className="text-body font-medium">{t(($) => $.knowledgeSeg.governance.findings)}</h4>
                  {findings.map((finding) => (
                    <section key={finding.id} className="border-b pb-3 last:border-b-0 last:pb-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <code className="min-w-0 flex-1 truncate text-caption">{finding.entry_key}</code>
                        <Badge variant="outline">{finding.verdict}</Badge>
                      </div>
                      <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-caption">
                        <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.governance.scope)}</dt>
                        <dd>{finding.applicable_scope}</dd>
                        <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.governance.recommendation)}</dt>
                        <dd>{finding.recommendation}</dd>
                        {finding.replacement_entry_id ? (
                          <>
                            <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.governance.replacement)}</dt>
                            <dd className="font-mono break-all">{finding.replacement_entry_id}</dd>
                          </>
                        ) : null}
                      </dl>
                      <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-muted/30 p-2 font-mono text-caption">
                        {finding.original_content}
                      </pre>
                      <div className="mt-2 space-y-1 text-caption text-muted-foreground">
                        {findingEvidence(finding).map((evidence, index) => (
                          <p key={`${finding.id}-${index}`} className="break-words">
                            {stringValue(evidence.source_type)} · {stringValue(evidence.immutable_ref)} · {stringValue(evidence.observed_at)}
                            {stringValue(evidence.excerpt) ? ` · ${stringValue(evidence.excerpt)}` : ""}
                          </p>
                        ))}
                      </div>
                    </section>
                  ))}
                </div>
              ) : null}

              {canManage ? (
                <div className="flex flex-wrap gap-2 border-t pt-3">
                  {current.status === "running" ? (
                    <Button size="sm" variant="outline" disabled={pending} onClick={() => onCancel(current)}>
                      {t(($) => $.knowledgeSeg.governance.cancel)}
                    </Button>
                  ) : (
                    <Button size="sm" variant="outline" disabled={pending} onClick={() => onRetry(current)}>
                      {t(($) => $.knowledgeSeg.governance.retry)}
                    </Button>
                  )}
                </div>
              ) : null}
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}
