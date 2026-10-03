"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Camera } from "lucide-react";
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
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
import { cn } from "@multica/ui/lib/utils";
import {
  promptGovernanceVersionsOptions,
  promptQualityDashboardOptions,
  useSnapshotPromptVersion,
  useSwitchPromptVersion,
} from "@multica/core/self-evolution";
import { agentListOptions, squadListOptions, skillListOptions } from "@multica/core/workspace/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { autopilotListOptions } from "@multica/core/autopilots/queries";
import { ApiError, clientErrorMessage } from "@multica/core/api";
import type {
  PromptGovernanceVersion,
  PromptSecretFinding,
} from "@multica/core/types";
import { useCurrentMember } from "@multica/core/permissions";
import { useT } from "../../i18n";
import { PromptDiffView } from "../../market/prompt-diff-view";

/**
 * The prompt version lifecycle tab (RUYI-285 rework). The self-evolution page
 * owns no content editing: a version is created by snapshotting the selected
 * carrier's currently effective configuration, and every effective change
 * still happens in the carrier's own feature entry. Activating a historical
 * version remains the copy-forward path, and nothing in the line is ever
 * rewritten.
 *
 * The picker is two-level — carrier type, then entity — because the version
 * line now covers all three prompt-carrying families: the four tiers plus
 * autopilot run prompts and skill bodies. The workspace tier needs no entity
 * pick: its effective content is this workspace's own.
 */
const SUBJECT_TYPES = [
  "workspace",
  "project",
  "squad",
  "agent",
  "autopilot",
  "skill",
] as const;

type SubjectType = (typeof SUBJECT_TYPES)[number];

export function VersionsTab({
  wsId,
  initialSubjectType = "agent",
  initialSubjectId = "",
}: {
  wsId: string;
  /** Preselects the carrier type. The page leaves the default; tests supply one. */
  initialSubjectType?: SubjectType;
  /** Preselects the entity (ignored for the workspace type). Empty = nothing picked. */
  initialSubjectId?: string;
}) {
  const { t } = useT("self-evolution");

  const [subjectType, setSubjectType] = useState<SubjectType>(initialSubjectType);
  const [subjectId, setSubjectId] = useState(
    initialSubjectType === "workspace" ? wsId : initialSubjectId,
  );
  const [selected, setSelected] = useState<number[]>([]);
  const [snapshotOpen, setSnapshotOpen] = useState(false);
  const [switchTarget, setSwitchTarget] = useState<PromptGovernanceVersion | null>(null);

  const currentMember = useCurrentMember(wsId);
  // Writes are owner-only on the server (router.go wires RequireWorkspaceRole
  // "owner"); admin is deliberately not included there, so not here either.
  const canManage = currentMember.role === "owner";

  const typeId = subjectType === "workspace" ? wsId : subjectId;

  // Each catalog loads only while its type is the picked one; an unpicked
  // list stays disabled and never fires.
  const agents = useQuery({ ...agentListOptions(wsId), enabled: subjectType === "agent" });
  const projects = useQuery({ ...projectListOptions(wsId), enabled: subjectType === "project" });
  const squads = useQuery({ ...squadListOptions(wsId), enabled: subjectType === "squad" });
  const autopilots = useQuery({
    ...autopilotListOptions(wsId),
    enabled: subjectType === "autopilot",
  });
  const skills = useQuery({ ...skillListOptions(wsId), enabled: subjectType === "skill" });

  const versions = useQuery(promptGovernanceVersionsOptions(wsId, subjectType, typeId));
  // A wide window: the badges only report which version the evaluation bound
  // each run to, and a longer window binds more versions.
  const quality = useQuery(promptQualityDashboardOptions(wsId, subjectType, typeId, 90));

  const snapshotVersion = useSnapshotPromptVersion(wsId, subjectType, typeId);
  const switchVersion = useSwitchPromptVersion(wsId, subjectType, typeId);

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

  const entityItems = useMemo(() => {
    if (subjectType === "workspace") {
      return [{ value: wsId, label: t(($) => $.versions.workspaceEntity) }];
    }
    const raw: { value: string; label: string }[] =
      subjectType === "agent"
        ? (agents.data ?? []).map((a) => ({ value: a.id, label: a.name }))
        : subjectType === "project"
          ? (projects.data ?? []).map((p) => ({ value: p.id, label: p.title }))
          : subjectType === "squad"
            ? (squads.data ?? []).map((s) => ({ value: s.id, label: s.name }))
            : subjectType === "autopilot"
              ? (autopilots.data ?? []).map((a) => ({ value: a.id, label: a.title }))
              : (skills.data ?? []).map((s) => ({ value: s.id, label: s.name }));
    return raw.sort((a, b) => a.label.localeCompare(b.label));
  }, [subjectType, wsId, t, agents.data, projects.data, squads.data, autopilots.data, skills.data]);

  // Stable identity across renders: the select refuses to open its popup
  // over an items array that changes identity on every render.
  const typeItems = useMemo(
    () => SUBJECT_TYPES.map((value) => ({ value, label: subjectTypeLabel(t, value) })),
    [t],
  );

  const latest = rows[0] ?? null;
  const left = findRow(rows, selected[0]);
  const right = findRow(rows, selected[1]);

  const pickType = (next: SubjectType) => {
    setSubjectType(next);
    setSubjectId(next === "workspace" ? wsId : "");
    // A different type is a different line; stale selections and dialogs
    // would otherwise render state from a carrier no longer on screen.
    setSelected([]);
    setSnapshotOpen(false);
    setSwitchTarget(null);
  };
  const pickEntity = (next: string) => {
    setSubjectId(next);
    setSelected([]);
    setSwitchTarget(null);
  };

  const snapshotBlocked = secretScanBody(snapshotVersion.error);
  const snapshotFailed =
    !snapshotBlocked && snapshotVersion.error !== null && snapshotVersion.error !== undefined;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-1.5">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.versions.subjectTypeLabel)}
            </span>
            <Select
              items={typeItems}
              value={subjectType}
              onValueChange={(next) => {
                if (typeof next === "string") pickType(next as SubjectType);
              }}
            >
              <SelectTrigger size="sm" className="w-40" aria-label={t(($) => $.versions.subjectTypeLabel)}>
                <SelectValue>{subjectTypeLabel(t, subjectType)}</SelectValue>
              </SelectTrigger>
              <SelectContent align="start">
                {SUBJECT_TYPES.map((value) => (
                  <SelectItem key={value} value={value}>
                    {subjectTypeLabel(t, value)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <span className="text-caption text-muted-foreground">
              {t(($) => $.versions.subjectEntityLabel)}
            </span>
            <Select
              items={entityItems}
              value={typeId}
              disabled={subjectType === "workspace"}
              onValueChange={(next) => {
                if (typeof next === "string") pickEntity(next);
              }}
            >
              <SelectTrigger size="sm" className="w-56" aria-label={t(($) => $.versions.subjectEntityLabel)}>
                <SelectValue>
                  {entityItems.find((i) => i.value === typeId)?.label ??
                    t(($) => $.versions.subjectEntityPlaceholder)}
                </SelectValue>
              </SelectTrigger>
              <SelectContent align="start" className="max-h-72">
                {entityItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
        {canManage ? (
          <Button
            size="sm"
            disabled={typeId === ""}
            onClick={() => setSnapshotOpen(true)}
          >
            <Camera className="h-4 w-4" />
            {t(($) => $.versions.snapshot)}
          </Button>
        ) : null}
      </div>

      {typeId === "" ? (
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

          {snapshotBlocked ? (
            <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2">
              <p className="text-caption font-medium">{t(($) => $.versions.secretBlocked)}</p>
              <ul className="mt-1 flex flex-col gap-0.5">
                {snapshotBlocked.findings.map((finding: PromptSecretFinding, index: number) => (
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
          ) : snapshotFailed ? (
            <p className="text-caption text-destructive">
              {t(($) => $.versions.snapshotFailed)}
              {clientErrorMessage(snapshotVersion.error)
                ? `: ${clientErrorMessage(snapshotVersion.error)}`
                : ""}
            </p>
          ) : null}

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

      <AlertDialog
        open={snapshotOpen}
        onOpenChange={(next) => {
          if (!next) setSnapshotOpen(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.versions.snapshotTitle)}</AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.versions.snapshotBody)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.versions.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                snapshotVersion.mutate(
                  {},
                  // change_note stays at its server default: the snapshot
                  // records what was effective, not an authored message.
                  {
                    onSuccess: () => {
                      setSnapshotOpen(false);
                      setSelected([]);
                    },
                  },
                );
              }}
            >
              {t(($) => $.versions.snapshotConfirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

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

function subjectTypeLabel(
  t: ReturnType<typeof useT<"self-evolution">>["t"],
  type: SubjectType,
): string {
  switch (type) {
    case "workspace":
      return t(($) => $.versions.typeWorkspace);
    case "project":
      return t(($) => $.versions.typeProject);
    case "squad":
      return t(($) => $.versions.typeSquad);
    case "agent":
      return t(($) => $.versions.typeAgent);
    case "autopilot":
      return t(($) => $.versions.typeAutopilot);
    case "skill":
      return t(($) => $.versions.typeSkill);
  }
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
  if (source === "snapshot") return t(($) => $.versions.sourceSnapshot);
  return source;
}
