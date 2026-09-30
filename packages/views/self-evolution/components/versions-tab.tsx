"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import {
  promptGovernanceVersionsOptions,
  promptQualityDashboardOptions,
  useSavePromptVersion,
  useSwitchPromptVersion,
} from "@multica/core/self-evolution";
import { agentDetailOptions, agentListOptions } from "@multica/core/workspace/queries";
import { ApiError, clientErrorMessage } from "@multica/core/api";
import type {
  Agent,
  PromptGovernanceVersion,
  PromptSecretFinding,
} from "@multica/core/types";
import { useCurrentMember } from "@multica/core/permissions";
import { useT } from "../../i18n";
import { PromptDiffView } from "../../market/prompt-diff-view";

/**
 * The prompt version lifecycle tab (RUYI-285): the entry the quality page
 * lacked. Every effective change is an append — the editor saves the text
 * below as the next version, activating a historical version copies it
 * forward, and nothing in the line is ever rewritten.
 *
 * The scope picker is agent-only for the same reason the quality and quiz
 * tabs are: the endpoint takes all four tiers, so widening this is a picker
 * change rather than a data change.
 */
const SCOPE = "agent";

export function VersionsTab({
  wsId,
  initialAgentId = "",
}: {
  wsId: string;
  /** Preselects the subject. The page leaves it empty; tests supply one. */
  initialAgentId?: string;
}) {
  const { t } = useT("self-evolution");

  const [agentId, setAgentId] = useState(initialAgentId);
  const [selected, setSelected] = useState<number[]>([]);
  const [editorOpen, setEditorOpen] = useState(false);
  const [switchTarget, setSwitchTarget] = useState<PromptGovernanceVersion | null>(null);

  const currentMember = useCurrentMember(wsId);
  // Writes are owner-only on the server (router.go wires RequireWorkspaceRole
  // "owner"); admin is deliberately not included there, so not here either.
  const canManage = currentMember.role === "owner";

  const agents = useQuery(agentListOptions(wsId));
  const versions = useQuery(promptGovernanceVersionsOptions(wsId, SCOPE, agentId));
  // A wide window: the badges only report which version the evaluation bound
  // each run to, and a longer window binds more versions.
  const quality = useQuery(promptQualityDashboardOptions(wsId, SCOPE, agentId, 90));
  const agentDetail = useQuery(agentDetailOptions(wsId, agentId));

  const saveVersion = useSavePromptVersion(wsId, SCOPE, agentId);
  const switchVersion = useSwitchPromptVersion(wsId, SCOPE, agentId);

  const rows = useMemo(
    () => [...(versions.data?.versions ?? [])].sort((a, b) => b.version - a.version),
    [versions.data],
  );
  const runsByVersion = useMemo(() => {
    const map = new Map<number, number>();
    for (const v of quality.data?.versions ?? []) {
      // Version 0 is the dashboard's window aggregate, not a row of the line.
      if (v.version > 0) map.set(v.version, v.runs);
    }
    return map;
  }, [quality.data]);

  const agentItems = useMemo(
    () => (agents.data ?? []).map((a: Agent) => ({ value: a.id, label: a.name })),
    [agents.data],
  );

  const latest = rows[0] ?? null;
  const left = findRow(rows, selected[0]);
  const right = findRow(rows, selected[1]);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
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
                setSelected([]);
                setEditorOpen(false);
                setSwitchTarget(null);
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
        {canManage ? (
          <Button size="sm" onClick={() => setEditorOpen(true)}>
            <Plus className="h-4 w-4" />
            {t(($) => $.versions.newVersion)}
          </Button>
        ) : null}
      </div>

      {agentId === "" ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t(($) => $.quality.noSubject.title)}</EmptyTitle>
            <EmptyDescription>{t(($) => $.quality.noSubject.description)}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <section className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-body font-medium">{t(($) => $.versions.title)}</h3>
            <span className="text-caption text-muted-foreground">
              {t(($) => $.versions.description)}
            </span>
            {selected.length > 0 ? (
              <Button size="sm" variant="ghost" onClick={() => setSelected([])}>
                {t(($) => $.versions.clearSelection)}
              </Button>
            ) : null}
          </div>
          <p className="text-caption text-muted-foreground">
            {t(($) => $.versions.bindingNote)}
          </p>

          {versions.isPending ? (
            <div className="flex flex-col gap-2">
              {Array.from({ length: 2 }, (_, i) => (
                <Skeleton key={i} className="h-16 w-full rounded-md" />
              ))}
            </div>
          ) : versions.isError ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t(($) => $.versions.error)}</EmptyTitle>
              </EmptyHeader>
              <Button size="sm" variant="outline" onClick={() => void versions.refetch()}>
                {t(($) => $.versions.retry)}
              </Button>
            </Empty>
          ) : rows.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t(($) => $.versions.emptyTitle)}</EmptyTitle>
                <EmptyDescription>{t(($) => $.versions.emptyDescription)}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="flex flex-col gap-2">
              {rows.map((v) => (
                <div
                  key={v.id}
                  className="flex flex-wrap items-start justify-between gap-3 rounded-md border px-3 py-2"
                >
                  <div className="flex min-w-0 flex-col gap-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <button
                        type="button"
                        aria-label={t(($) => $.quality.timeline.version, { version: v.version })}
                        onClick={() => setSelected((prev) => toggleVersion(prev, v.version))}
                        className={cn(
                          "rounded-md border px-2 py-1 text-caption font-medium tabular-nums transition-colors",
                          selected.includes(v.version)
                            ? "border-primary bg-muted"
                            : "border-border hover:bg-muted/50",
                        )}
                      >
                        {t(($) => $.quality.timeline.version, { version: v.version })}
                      </button>
                      <Badge variant="outline">{sourceLabel(t, v.source)}</Badge>
                      {v.source === "revert" && v.source_version !== undefined ? (
                        <span className="text-caption text-muted-foreground">
                          {t(($) => $.versions.fromVersion, { version: v.source_version })}
                        </span>
                      ) : null}
                      {latest !== null && v.version === latest.version ? (
                        <Badge variant="secondary">{t(($) => $.versions.latest)}</Badge>
                      ) : null}
                    </div>
                    {v.change_note !== "" ? <p className="text-caption">{v.change_note}</p> : null}
                    <p className="text-caption text-muted-foreground tabular-nums">
                      {v.created_at.slice(0, 10)} · {v.content_sha256.slice(0, 8)}
                    </p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline" className="tabular-nums">
                      {(runsByVersion.get(v.version) ?? 0) > 0
                        ? t(($) => $.versions.runs, { n: runsByVersion.get(v.version) ?? 0 })
                        : t(($) => $.versions.noRuns)}
                    </Badge>
                    {canManage && latest !== null && v.version !== latest.version ? (
                      <Button size="sm" variant="outline" onClick={() => setSwitchTarget(v)}>
                        {t(($) => $.versions.activate)}
                      </Button>
                    ) : null}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      )}

      <NewVersionDialog
        open={editorOpen}
        pending={saveVersion.isPending}
        currentContent={agentDetail.data?.instructions ?? ""}
        error={saveVersion.error}
        onOpenChange={setEditorOpen}
        onSubmit={(draft) => {
          saveVersion.mutate(draft, {
            onSuccess: () => {
              setEditorOpen(false);
              setSelected([]);
            },
          });
        }}
      />

      <AlertDialog
        open={switchTarget !== null}
        onOpenChange={(next) => {
          if (!next) setSwitchTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.versions.activateTitle, { version: switchTarget?.version ?? 0 })}
            </AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.versions.activateBody)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.versions.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (switchTarget === null) return;
                switchVersion.mutate(switchTarget.version, {
                  onSuccess: () => setSwitchTarget(null),
                });
              }}
            >
              {t(($) => $.versions.confirmActivate)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {left !== null && right !== null ? (
        <Dialog
          open
          onOpenChange={(next) => {
            if (!next) setSelected([]);
          }}
        >
          <DialogContent className="sm:max-w-3xl">
            <DialogHeader>
              <DialogTitle>
                {t(($) => $.versions.compareTitle, { from: left.version, to: right.version })}
              </DialogTitle>
              <DialogDescription>{t(($) => $.versions.description)}</DialogDescription>
            </DialogHeader>
            <PromptDiffView current={left.content} incoming={right.content} />
          </DialogContent>
        </Dialog>
      ) : null}
    </div>
  );
}

/**
 * The editor that turns the tier's current effective text into the next
 * version. The textarea preloads the agent's live instructions; the first
 * keystroke forks the draft, so a refetch landing mid-edit cannot clobber it.
 */
function NewVersionDialog({
  open,
  pending,
  currentContent,
  error,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  pending: boolean;
  currentContent: string;
  error: unknown;
  onOpenChange: (open: boolean) => void;
  onSubmit: (draft: { content: string; change_note: string }) => void;
}) {
  const { t } = useT("self-evolution");
  // null = untouched, rendering the live content; a string = a forked draft.
  const [draft, setDraft] = useState<string | null>(null);
  const [note, setNote] = useState("");

  useEffect(() => {
    if (open) {
      setDraft(null);
      setNote("");
    }
  }, [open]);

  const content = draft ?? currentContent;
  const blocked = secretScanBody(error);
  const failed = !blocked && error !== null && error !== undefined;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.versions.editorTitle)}</DialogTitle>
          <DialogDescription>{t(($) => $.versions.editorDescription)}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1.5">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.versions.contentLabel)}
            </span>
            <Textarea
              rows={14}
              className="font-mono text-caption"
              value={content}
              onChange={(e) => setDraft(e.target.value)}
            />
            <span className="text-caption text-muted-foreground">
              {t(($) => $.versions.contentHint)}
            </span>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.versions.noteLabel)}
            </span>
            <Input
              value={note}
              placeholder={t(($) => $.versions.notePlaceholder)}
              onChange={(e) => setNote(e.target.value)}
            />
          </label>
          {blocked ? (
            <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2">
              <p className="text-caption font-medium">{t(($) => $.versions.secretBlocked)}</p>
              <ul className="mt-1 flex flex-col gap-0.5">
                {blocked.findings.map((finding: PromptSecretFinding, index: number) => (
                  <li
                    key={`${finding.rule}-${finding.line}-${index}`}
                    className="flex flex-wrap items-baseline gap-x-2 text-caption"
                  >
                    <span className="font-medium">{finding.category}</span>
                    <span className="text-muted-foreground">{finding.rule}</span>
                    <span className="text-muted-foreground tabular-nums">
                      {t(($) => $.versions.findingLine, { line: finding.line })}
                    </span>
                    {/* `mask` is a fixed-width mask minted server-side, never
                        a prefix of the matched value. */}
                    <span className="font-mono text-faint-foreground">{finding.mask}</span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {failed ? (
            <p className="text-caption text-destructive">
              {t(($) => $.versions.saveFailed)}
              {clientErrorMessage(error) ? `: ${clientErrorMessage(error)}` : ""}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.versions.cancel)}
          </Button>
          <Button
            disabled={pending || content.trim() === ""}
            onClick={() => onSubmit({ content, change_note: note })}
          >
            {pending ? t(($) => $.versions.saving) : t(($) => $.versions.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The 422 body a secret-scan block returns, or null for any other failure. */
function secretScanBody(
  error: unknown,
): { findings: PromptSecretFinding[]; truncated: boolean } | null {
  if (!(error instanceof ApiError) || error.status !== 422) return null;
  const body = error.body;
  if (!body || typeof body !== "object") return null;
  const candidate = body as { code?: unknown; findings?: unknown; truncated?: unknown };
  if (candidate.code !== "prompt_secret_detected") return null;
  return {
    findings: Array.isArray(candidate.findings)
      ? (candidate.findings as PromptSecretFinding[])
      : [],
    truncated: candidate.truncated === true,
  };
}

function findRow(
  rows: PromptGovernanceVersion[],
  version: number | undefined,
): PromptGovernanceVersion | null {
  if (version === undefined) return null;
  return rows.find((v) => v.version === version) ?? null;
}

/** Two at a time; picking a third drops the older selection. */
function toggleVersion(prev: number[], version: number): number[] {
  if (prev.includes(version)) return prev.filter((v) => v !== version);
  if (prev.length < 2) return [...prev, version];
  return [prev[1] as number, version];
}

function sourceLabel(t: ReturnType<typeof useT<"self-evolution">>["t"], source: string): string {
  if (source === "import") return t(($) => $.versions.sourceImport);
  if (source === "edit") return t(($) => $.versions.sourceEdit);
  if (source === "revert") return t(($) => $.versions.sourceRevert);
  if (source === "auto_snapshot") return t(($) => $.versions.sourceAuto);
  return source;
}
