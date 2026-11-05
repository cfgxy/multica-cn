"use client";

import { useEffect, useMemo, useState } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Markdown } from "@multica/ui/markdown";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@multica/ui/components/ui/sheet";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@multica/ui/components/ui/alert-dialog";
import { clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import type { KnowledgeDir, KnowledgeEntry, KnowledgeEntryOperation } from "@multica/core/types";
import {
  knowledgeDirsOptions,
  knowledgeEntriesOptions,
  useAdoptKnowledgeEntry,
  useDeleteKnowledgeEntry,
  useEditKnowledgeEntry,
  useMergeKnowledgeEntries,
  useRegisterKnowledgeDir,
  useScanKnowledgeDir,
  useUnregisterKnowledgeDir,
} from "@multica/core/self-evolution";
import { useLocale, useT } from "../../i18n";
import { EntitySegToolbar } from "./entity-seg-toolbar";
import { SelfEvolutionShell } from "./self-evolution-shell";
import { useWorkspaceId } from "@multica/core/hooks";
import { ContentEditor } from "../../editor";

type KnowledgeSegment = "entries" | "dirs" | "external";

/**
 * The knowledge page (RUYI-551 §2.3, reworking RUYI-265 §K): three segments
 * — entries (the browsing default), registered dirs, and external candidate
 * sources — so operations no longer crowd the first screen (walk #18).
 * Adding an external source is the page-level CTA, no longer a collapsed
 * ghost at the bottom (walk #3). Rows open the full entry reader or the dir
 * drawer with its status closure and scan history (walks #4–6). Writes stay
 * owner-only, exactly as the server enforces.
 */
export function KnowledgePage() {
  const wsId = useWorkspaceId();
  return (
    <SelfEvolutionShell>
      <KnowledgePageBody wsId={wsId} />
    </SelfEvolutionShell>
  );
}

export function KnowledgePageBody({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const locale = useLocale();
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const [segment, setSegment] = useState<KnowledgeSegment>("entries");
  const [dirId, setDirId] = useState("");
  const [query, setQuery] = useState("");
  const [openEntry, setOpenEntry] = useState<KnowledgeEntry | null>(null);
  const [openDir, setOpenDir] = useState<KnowledgeDir | null>(null);
  const [registerOpen, setRegisterOpen] = useState(false);

  const dirs = useQuery(knowledgeDirsOptions(wsId));
  const entries = useQuery(knowledgeEntriesOptions(wsId, dirId, query));
  const entryCandidates = useQuery({
    ...knowledgeEntriesOptions(wsId, openEntry?.dir_id ?? "", ""),
    enabled: openEntry !== null,
  });
  const register = useRegisterKnowledgeDir(wsId);
  const scan = useScanKnowledgeDir(wsId);
  const unregister = useUnregisterKnowledgeDir(wsId);
  const adopt = useAdoptKnowledgeEntry(wsId);
  const edit = useEditKnowledgeEntry(wsId);
  const remove = useDeleteKnowledgeEntry(wsId);
  const merge = useMergeKnowledgeEntries(wsId);

  const mutationError = (e: unknown) =>
    toast.error(clientErrorMessage(e) ?? t(($) => $.knowledge.error.write));

  const dirList = dirs.data ?? [];
  const externalDirs = dirList.filter((d) => d.kind !== "ultimate");
  const dirLabel = (id: string) => dirList.find((d) => d.id === id)?.label ?? id;

  const kindLabel = (kind: string) => {
    switch (kind) {
      case "ultimate": return t(($) => $.knowledge.kind.ultimate);
      case "candidate_cli": return t(($) => $.knowledge.kind.candidate_cli);
      case "candidate_auto": return t(($) => $.knowledge.kind.candidate_auto);
      default: return kind;
    }
  };
  const mirrorLabel = (state: string) => {
    switch (state) {
      case "synced": return t(($) => $.knowledge.mirrorState.synced);
      case "source_deleted": return t(($) => $.knowledge.mirrorState.source_deleted);
      case "source_removed": return t(($) => $.knowledge.mirrorState.source_removed);
      default: return state;
    }
  };
  const adoptionLabel = (state: string) => {
    switch (state) {
      case "pending": return t(($) => $.knowledge.adoptionState.pending);
      case "transferring": return t(($) => $.knowledge.adoptionState.transferring);
      case "adopted": return t(($) => $.knowledge.adoptionState.adopted);
      case "failed": return t(($) => $.knowledge.adoptionState.failed);
      default: return state;
    }
  };
  const scanResultLabel = (result: string) => {
    switch (result) {
      case "noop": return t(($) => $.knowledge.scanResult.noop);
      case "changed": return t(($) => $.knowledge.scanResult.changed);
      case "failed": return t(($) => $.knowledge.scanResult.failed);
      default: return result;
    }
  };
  const healthLabel = (state: string) =>
    state === "ok"
      ? t(($) => $.knowledge.health.ok)
      : t(($) => $.knowledge.health.unhealthy);

  const segmentRows: { value: KnowledgeSegment; label: string; count?: number }[] = [
    { value: "entries", label: t(($) => $.knowledgeSeg.entries) },
    { value: "dirs", label: t(($) => $.knowledgeSeg.dirs), count: dirList.length },
    {
      value: "external",
      label: t(($) => $.knowledgeSeg.external),
      count: externalDirs.length,
    },
  ];

  const openEntriesUnderDir = (dir: KnowledgeDir) => {
    setOpenDir(null);
    setDirId(dir.id);
    setSegment("entries");
  };

  return (
    <div className="flex min-w-0 flex-col gap-4 overflow-y-auto">
      <EntitySegToolbar
        segments={segmentRows}
        value={segment}
        onValueChange={(next) => setSegment(next as KnowledgeSegment)}
        action={
          canManage ? (
            <Button
              size="sm"
              data-testid="knowledge-add-source"
              onClick={() => setRegisterOpen(true)}
            >
              <Plus className="mr-1 size-4" aria-hidden />
              {t(($) => $.knowledgeSeg.addSource)}
            </Button>
          ) : null
        }
        filters={
          segment === "entries" ? (
            <>
              <Select
                items={[
                  { value: "", label: t(($) => $.knowledge.allDirs) },
                  ...dirList.map((d) => ({ value: d.id, label: d.label })),
                ]}
                value={dirId}
                onValueChange={(next) => {
                  if (typeof next === "string") setDirId(next);
                }}
              >
                <SelectTrigger size="sm" className="w-48">
                  <SelectValue>
                    {dirId === ""
                      ? t(($) => $.knowledge.allDirs)
                      : dirList.find((d) => d.id === dirId)?.label ?? t(($) => $.knowledge.allDirs)}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">{t(($) => $.knowledge.allDirs)}</SelectItem>
                  {dirList.map((d) => (
                    <SelectItem key={d.id} value={d.id}>
                      {d.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Input
                className="w-56"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t(($) => $.knowledge.searchPlaceholder)}
              />
            </>
          ) : null
        }
      />

      {segment === "entries" ? (
        <EntriesSegment
          entries={entries}
          dirLabel={dirLabel}
          mirrorLabel={mirrorLabel}
          adoptionLabel={adoptionLabel}
          canManage={canManage}
          adoptPending={adopt.isPending}
          onOpenEntry={setOpenEntry}
          onAdopt={(entry) => adopt.mutate(entry.id, { onError: mutationError })}
          onRetry={() => void entries.refetch()}
        />
      ) : null}

      {segment === "dirs" ? (
        <DirsSegment
          dirs={dirList}
          dirsQuery={dirs}
          kindLabel={kindLabel}
          healthLabel={healthLabel}
          scanResultLabel={scanResultLabel}
          canManage={canManage}
          scanPending={scan.isPending}
          unregisterPending={unregister.isPending}
          onOpenDir={setOpenDir}
          onScan={(dir) => scan.mutate(dir.id, { onError: mutationError })}
          onUnregister={(dir) => unregister.mutate(dir.id, { onError: mutationError })}
        />
      ) : null}

      {segment === "external" ? (
        <DirsSegment
          dirs={externalDirs}
          dirsQuery={dirs}
          external
          kindLabel={kindLabel}
          healthLabel={healthLabel}
          scanResultLabel={scanResultLabel}
          canManage={canManage}
          scanPending={scan.isPending}
          unregisterPending={unregister.isPending}
          onOpenDir={setOpenDir}
          onScan={(dir) => scan.mutate(dir.id, { onError: mutationError })}
          onUnregister={(dir) => unregister.mutate(dir.id, { onError: mutationError })}
          onJumpToDirs={() => setSegment("dirs")}
        />
      ) : null}

      <KnowledgeEntryDialog
        entry={openEntry}
        entries={entryCandidates.data ?? []}
        ultimateDirIds={new Set(dirList.filter((dir) => dir.kind === "ultimate").map((dir) => dir.id))}
        isOpen={openEntry !== null}
        canManage={canManage}
        dirLabel={dirLabel}
        mirrorLabel={mirrorLabel}
        adoptionLabel={adoptionLabel}
        locale={locale}
        adoptPending={adopt.isPending}
        writePending={edit.isPending || remove.isPending || merge.isPending}
        onOpenChange={(open) => {
          if (!open) setOpenEntry(null);
        }}
        onAdopt={(entry) => adopt.mutate(entry.id, { onError: mutationError, onSuccess: () => setOpenEntry(null) })}
        onEdit={(id, content) => edit.mutate({ id, content }, { onError: mutationError, onSuccess: () => setOpenEntry(null) })}
        onDelete={(id) => remove.mutate(id, { onError: mutationError, onSuccess: () => setOpenEntry(null) })}
        onMerge={(id, content, sourceEntryIds) => merge.mutate(
          { id, content, sourceEntryIds },
          { onError: mutationError, onSuccess: () => setOpenEntry(null) },
        )}
      />

      {/* Dir drawer (fig 4, 440px): status closure strip, attributes, scan
          history, and the owner actions — unregister behind a confirm. */}
      <Sheet
        open={openDir !== null}
        onOpenChange={(open) => {
          if (!open) setOpenDir(null);
        }}
      >
        <SheetContent className="w-full gap-0 overflow-y-auto sm:max-w-[440px]" data-testid="knowledge-dir-drawer">
          {openDir ? (
            <>
              <SheetHeader>
                <SheetTitle>{openDir.label}</SheetTitle>
                <SheetDescription className="font-mono">{openDir.path}</SheetDescription>
              </SheetHeader>
              <div className="flex flex-col gap-4 px-4 py-4">
                <div
                  className="flex flex-wrap items-center gap-2 rounded-md border px-3 py-2"
                  data-testid="knowledge-dir-status"
                >
                  <span
                    aria-hidden
                    className={`inline-block size-2 rounded-full ${openDir.health_state === "ok" ? "bg-emerald-500" : "bg-destructive"}`}
                    data-status={openDir.health_state}
                  />
                  <span className="text-body font-medium">{healthLabel(openDir.health_state)}</span>
                  {openDir.scan_requested ? (
                    <Badge variant="outline">{t(($) => $.knowledge.scanRequested)}</Badge>
                  ) : null}
                  {openDir.health_note ? (
                    <span className="w-full text-caption text-muted-foreground">
                      {openDir.health_note}
                    </span>
                  ) : null}
                </div>
                <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-caption">
                  <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.attrKind)}</dt>
                  <dd>{kindLabel(openDir.kind)}</dd>
                  <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.attrEntries)}</dt>
                  <dd>{openDir.entry_count}</dd>
                  <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.attrCreated)}</dt>
                  <dd>{new Date(openDir.created_at).toLocaleDateString(locale)}</dd>
                </dl>
                <div className="space-y-1.5">
                  <h4 className="text-body font-medium">{t(($) => $.knowledgeSeg.scanHistory)}</h4>
                  {openDir.last_scan ? (
                    <p className="text-caption text-muted-foreground">
                      {t(($) => $.knowledge.lastScan, {
                        result: scanResultLabel(openDir.last_scan.result),
                        added: openDir.last_scan.added,
                        updated: openDir.last_scan.updated,
                        removed: openDir.last_scan.removed,
                      })}
                      {" · "}
                      {new Date(openDir.last_scan.started_at).toLocaleString(locale)}
                    </p>
                  ) : (
                    <p className="text-caption text-muted-foreground">
                      {t(($) => $.knowledge.neverScanned)}
                    </p>
                  )}
                </div>
                <div className="flex flex-wrap gap-2 border-t pt-3">
                  <Button size="sm" variant="outline" onClick={() => openEntriesUnderDir(openDir)}>
                    {t(($) => $.knowledgeSeg.viewEntries)}
                  </Button>
                  {canManage && openDir.kind !== "ultimate" ? (
                    <>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={scan.isPending}
                        onClick={() => scan.mutate(openDir.id, { onError: mutationError })}
                      >
                        {t(($) => $.knowledge.scan)}
                      </Button>
                      <UnregisterButton
                        pending={unregister.isPending}
                        label={t(($) => $.knowledge.unregister)}
                        confirmLabel={t(($) => $.knowledge.unregister)}
                        cancelLabel={t(($) => $.skills.cancel)}
                        title={t(($) => $.knowledge.unregister)}
                        description={t(($) => $.knowledgeSeg.unregisterConfirm, { label: openDir.label })}
                        onConfirm={() =>
                          unregister.mutate(openDir.id, {
                            onError: mutationError,
                            onSuccess: () => setOpenDir(null),
                          })
                        }
                      />
                    </>
                  ) : null}
                </div>
              </div>
            </>
          ) : null}
        </SheetContent>
      </Sheet>

      {canManage ? (
        <RegisterSourceDialog
          open={registerOpen}
          pending={register.isPending}
          onOpenChange={setRegisterOpen}
          onSubmit={(draft) => {
            register.mutate(draft, {
              onError: mutationError,
              onSuccess: () => {
                setRegisterOpen(false);
                setSegment("dirs");
              },
            });
          }}
        />
      ) : null}
    </div>
  );
}

function KnowledgeEntryDialog({
  entry,
  entries,
  ultimateDirIds,
  isOpen,
  canManage,
  dirLabel,
  mirrorLabel,
  adoptionLabel,
  locale,
  adoptPending,
  writePending,
  onOpenChange,
  onAdopt,
  onEdit,
  onDelete,
  onMerge,
}: {
  entry: KnowledgeEntry | null;
  entries: KnowledgeEntry[];
  ultimateDirIds: Set<string>;
  isOpen: boolean;
  canManage: boolean;
  dirLabel: (id: string) => string;
  mirrorLabel: (state: string) => string;
  adoptionLabel: (state: string) => string;
  locale: string;
  adoptPending: boolean;
  writePending: boolean;
  onOpenChange: (open: boolean) => void;
  onAdopt: (entry: KnowledgeEntry) => void;
  onEdit: (id: string, content: string) => void;
  onDelete: (id: string) => void;
  onMerge: (id: string, content: string, sourceEntryIds: string[]) => void;
}) {
  const { t } = useT("self-evolution");
  const [mode, setMode] = useState<"read" | "edit" | "merge">("read");
  const [draft, setDraft] = useState("");
  const [selectedSourceIds, setSelectedSourceIds] = useState<string[]>([]);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [mergeOpen, setMergeOpen] = useState(false);
  const entryID = entry?.id;
  const entryContent = entry?.content ?? "";

  useEffect(() => {
    if (!entryID) return;
    setMode("read");
    setDraft(entryContent);
    setSelectedSourceIds([]);
    setDeleteOpen(false);
    setMergeOpen(false);
  }, [entryContent, entryID]);

  const mergeCandidates = useMemo(() => {
    if (!entry) return [];
    return entries
      .filter((candidate) => candidate.id !== entry.id && candidate.dir_id === entry.dir_id)
      .filter((candidate) => candidate.mirror_state === "synced")
      .filter((candidate) => candidate.pending_operation === undefined)
      .map((candidate) => ({
        entry: candidate,
        kind: candidate.content_sha256 === entry.content_sha256
          ? "duplicate"
          : knowledgeSimilarity(entry.content, candidate.content) >= 0.45
            ? "similar"
            : null,
      }))
      .filter((candidate): candidate is { entry: KnowledgeEntry; kind: "duplicate" | "similar" } => candidate.kind !== null);
  }, [entries, entry]);

  const writable = entry !== null
    && canManage
    && ultimateDirIds.has(entry.dir_id)
    && entry.mirror_state === "synced"
    && entry.pending_operation === undefined;
  const value = entry ? knowledgeValue(entry.content) : null;
  const close = () => onOpenChange(false);

  return (
    <Dialog open={isOpen} onOpenChange={onOpenChange}>
      <DialogContent
        className="flex h-[min(90vh,56rem)] max-w-5xl flex-col overflow-hidden p-0"
        data-testid="knowledge-entry-dialog"
      >
        {entry ? (
          <>
            <DialogHeader className="border-b px-6 py-5">
              <div className="flex min-w-0 flex-wrap items-center gap-2 pr-8">
                <DialogTitle className="truncate font-mono text-title-sm">{entry.key}</DialogTitle>
                <Badge variant="outline">{mirrorLabel(entry.mirror_state)}</Badge>
                <Badge variant={entry.adoption_state === "adopted" ? "secondary" : "outline"}>
                  {adoptionLabel(entry.adoption_state)}
                </Badge>
              </div>
              <DialogDescription>
                {t(($) => $.knowledgeSeg.entryDrawerSubtitle, { dir: dirLabel(entry.dir_id) })}
              </DialogDescription>
            </DialogHeader>
            <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
              <div className="mx-auto flex w-full max-w-4xl flex-col gap-5">
                <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-caption">
                  <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.attrDir)}</dt>
                  <dd>{dirLabel(entry.dir_id)}</dd>
                  <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.attrUpdated)}</dt>
                  <dd>{new Date(entry.last_confirmed_at).toLocaleString(locale)}</dd>
                  {value ? (
                    <>
                      <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.attrValue)}</dt>
                      <dd>
                        <Badge variant="outline">
                          {t(($) => value === "high" ? $.knowledgeSeg.highValue : $.knowledgeSeg.lowValue)}
                        </Badge>
                      </dd>
                    </>
                  ) : null}
                  {entry.adopted_from_key ? (
                    <>
                      <dt className="text-muted-foreground">{t(($) => $.knowledgeSeg.attrSource)}</dt>
                      <dd className="font-mono">{entry.adopted_from_key}</dd>
                    </>
                  ) : null}
                </dl>
                {entry.adoption_error ? (
                  <p role="alert" className="text-caption text-destructive">{entry.adoption_error}</p>
                ) : null}
                {entry.pending_operation ? (
                  <KnowledgeOperationStatus operation={entry.pending_operation} detailed />
                ) : null}
                {mode === "read" ? (
                  <div className="min-h-64 rounded-md border bg-background p-5" data-testid="knowledge-entry-markdown">
                    <Markdown mode="full">{entry.content}</Markdown>
                  </div>
                ) : (
                  <div className="min-h-80 rounded-md border bg-background p-3" data-testid="knowledge-entry-editor">
                    <ContentEditor
                      value={draft}
                      onUpdate={(content) => setDraft(content)}
                      disableMentions
                      showBubbleMenu
                      placeholder={t(($) => $.knowledgeSeg.editorPlaceholder)}
                    />
                  </div>
                )}
                {mode === "merge" ? (
                  <div className="space-y-3 rounded-md border p-4" data-testid="knowledge-merge-candidates">
                    <div>
                      <h3 className="text-body font-medium">{t(($) => $.knowledgeSeg.mergeCandidates)}</h3>
                      <p className="text-caption text-muted-foreground">{t(($) => $.knowledgeSeg.mergeDescription)}</p>
                    </div>
                    {mergeCandidates.length ? mergeCandidates.map(({ entry: candidate, kind }) => {
                      const checked = selectedSourceIds.includes(candidate.id);
                      return (
                        <label key={candidate.id} className="flex cursor-pointer items-start gap-3 rounded-md px-2 py-2 hover:bg-muted">
                          <Checkbox
                            checked={checked}
                            onCheckedChange={(next) => setSelectedSourceIds((current) =>
                              next === true ? [...current, candidate.id] : current.filter((id) => id !== candidate.id),
                            )}
                          />
                          <span className="min-w-0 flex-1">
                            <span className="flex items-center gap-2">
                              <span className="truncate font-mono text-caption font-medium">{candidate.key}</span>
                              <Badge variant="outline">{t(($) => kind === "duplicate" ? $.knowledgeSeg.duplicate : $.knowledgeSeg.similar)}</Badge>
                            </span>
                            <span className="line-clamp-2 text-caption text-muted-foreground">{candidate.content}</span>
                          </span>
                        </label>
                      );
                    }) : <p className="text-caption text-muted-foreground">{t(($) => $.knowledgeSeg.noMergeCandidates)}</p>}
                  </div>
                ) : null}
              </div>
            </div>
            <DialogFooter className="border-t px-6 py-4 sm:justify-between">
              <div className="flex flex-wrap gap-2">
                {writable && mode === "read" ? (
                  <>
                    <Button size="sm" variant="outline" onClick={() => setMode("edit")}>
                      <Pencil className="mr-1 size-4" aria-hidden />
                      {t(($) => $.knowledgeSeg.edit)}
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => setMode("merge")}>
                      {t(($) => $.knowledgeSeg.merge)}
                    </Button>
                    <Button size="sm" variant="outline" className="text-destructive hover:text-destructive" onClick={() => setDeleteOpen(true)}>
                      <Trash2 className="mr-1 size-4" aria-hidden />
                      {t(($) => $.knowledgeSeg.delete)}
                    </Button>
                  </>
                ) : null}
                {canManage && entry.adoption_state === "pending" ? (
                  <Button size="sm" disabled={adoptPending} onClick={() => onAdopt(entry)}>{t(($) => $.knowledge.adopt)}</Button>
                ) : null}
              </div>
              <div className="flex gap-2">
                {mode !== "read" ? <Button size="sm" variant="outline" onClick={() => setMode("read")}>{t(($) => $.knowledge.cancel)}</Button> : null}
                {mode === "edit" ? (
                  <Button size="sm" disabled={writePending || draft.trim() === "" || draft === entry.content} onClick={() => onEdit(entry.id, draft)}>{t(($) => $.knowledgeSeg.save)}</Button>
                ) : null}
                {mode === "merge" ? (
                  <Button size="sm" disabled={writePending || draft.trim() === "" || selectedSourceIds.length === 0} onClick={() => setMergeOpen(true)}>{t(($) => $.knowledgeSeg.merge)}</Button>
                ) : null}
                {mode === "read" ? <Button size="sm" variant="outline" onClick={close}>{t(($) => $.knowledge.cancel)}</Button> : null}
              </div>
            </DialogFooter>
            <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t(($) => $.knowledgeSeg.delete)}</AlertDialogTitle>
                  <AlertDialogDescription>{t(($) => $.knowledgeSeg.deleteConfirm, { key: entry.key })}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t(($) => $.knowledge.cancel)}</AlertDialogCancel>
                  <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/90" onClick={() => onDelete(entry.id)}>{t(($) => $.knowledgeSeg.delete)}</AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
            <AlertDialog open={mergeOpen} onOpenChange={setMergeOpen}>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t(($) => $.knowledgeSeg.merge)}</AlertDialogTitle>
                  <AlertDialogDescription>{t(($) => $.knowledgeSeg.mergeConfirm, { count: selectedSourceIds.length })}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t(($) => $.knowledge.cancel)}</AlertDialogCancel>
                  <AlertDialogAction onClick={() => onMerge(entry.id, draft, selectedSourceIds)}>{t(($) => $.knowledgeSeg.merge)}</AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function KnowledgeOperationStatus({
  operation,
  detailed = false,
}: {
  operation: KnowledgeEntryOperation;
  detailed?: boolean;
}) {
  const { t } = useT("self-evolution");
  const operationLabel = (() => {
    switch (operation.operation_type) {
      case "edit": return t(($) => $.knowledgeSeg.operationType.edit);
      case "delete": return t(($) => $.knowledgeSeg.operationType.delete);
      case "merge": return t(($) => $.knowledgeSeg.operationType.merge);
      default: return t(($) => $.knowledgeSeg.operationType.change);
    }
  })();
  const hasError = operation.error !== undefined && operation.error !== "";

  return (
    <div
      className={detailed ? "rounded-md border border-destructive/30 bg-destructive/5 p-3" : undefined}
      data-testid="knowledge-entry-operation"
    >
      <Badge variant={hasError ? "destructive" : "outline"}>
        {hasError
          ? t(($) => $.knowledgeSeg.operationFailed, { operation: operationLabel })
          : t(($) => $.knowledgeSeg.operationSyncing, { operation: operationLabel })}
      </Badge>
      {detailed && hasError ? (
        <p role="alert" className="mt-2 break-words text-caption text-destructive">
          {operation.error}
        </p>
      ) : null}
    </div>
  );
}

function knowledgeSimilarity(left: string, right: string): number {
  const words = (content: string) => new Set(content.toLowerCase().match(/[\p{L}\p{N}_-]{3,}/gu) ?? []);
  const leftWords = words(left);
  const rightWords = words(right);
  if (!leftWords.size || !rightWords.size) return 0;
  let intersection = 0;
  for (const word of leftWords) {
    if (rightWords.has(word)) intersection++;
  }
  return intersection / (leftWords.size + rightWords.size - intersection);
}

function knowledgeValue(content: string): "high" | "low" | null {
  const normalized = content.trim();
  const paragraphs = normalized.split(/\n\s*\n/).filter(Boolean).length;
  const hasStructure = /^#{1,6}\s|^\s*(?:[-*+]|\d+\.)\s+/m.test(normalized);

  if (normalized.length <= 140 && paragraphs <= 1 && !hasStructure) return "low";
  if (normalized.length >= 500 && (paragraphs >= 3 || hasStructure)) return "high";
  return null;
}

function EntriesSegment({
  entries,
  dirLabel,
  mirrorLabel,
  adoptionLabel,
  canManage,
  adoptPending,
  onOpenEntry,
  onAdopt,
  onRetry,
}: {
  entries: UseKnowledgeEntries;
  dirLabel: (id: string) => string;
  mirrorLabel: (state: string) => string;
  adoptionLabel: (state: string) => string;
  canManage: boolean;
  adoptPending: boolean;
  onOpenEntry: (entry: KnowledgeEntry) => void;
  onAdopt: (entry: KnowledgeEntry) => void;
  onRetry: () => void;
}) {
  const { t } = useT("self-evolution");
  if (entries.isPending) return <Skeleton className="h-40 w-full" />;
  if (entries.isError) {
    return (
      <p role="alert" className="text-body text-destructive">
        {t(($) => $.knowledge.error.read)}
        <Button size="sm" variant="outline" className="ml-2" onClick={onRetry}>
          {t(($) => $.knowledge.error.retry)}
        </Button>
      </p>
    );
  }
  if (!entries.data?.length) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>{t(($) => $.knowledge.emptyDirs.title)}</EmptyTitle>
          <EmptyDescription>{t(($) => $.knowledge.emptyDirs.description)}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  return (
    <div className="overflow-hidden rounded-lg border" data-testid="knowledge-entries-table">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[28%]">{t(($) => $.knowledgeSeg.colKey)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.attrDir)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.attrMirror)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.attrAdoption)}</TableHead>
            <TableHead className="w-[10rem] text-right">{t(($) => $.knowledgeSeg.colActions)}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.data.map((entry) => {
            const value = knowledgeValue(entry.content);
            return (
              <TableRow
                key={entry.id}
                className="cursor-pointer"
                data-testid={`knowledge-entry-${entry.key}`}
                onClick={() => onOpenEntry(entry)}
              >
                <TableCell className="max-w-0">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-mono text-caption font-medium">{entry.key}</span>
                    {value ? (
                      <Badge variant="outline">
                        {t(($) => value === "high" ? $.knowledgeSeg.highValue : $.knowledgeSeg.lowValue)}
                      </Badge>
                    ) : null}
                  </div>
                </TableCell>
              <TableCell className="max-w-0 truncate text-caption text-muted-foreground">
                {dirLabel(entry.dir_id)}
              </TableCell>
              <TableCell>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline">{mirrorLabel(entry.mirror_state)}</Badge>
                  {entry.pending_operation ? (
                    <KnowledgeOperationStatus operation={entry.pending_operation} />
                  ) : null}
                </div>
              </TableCell>
              <TableCell>
                <Badge variant={entry.adoption_state === "adopted" ? "secondary" : "outline"}>
                  {adoptionLabel(entry.adoption_state)}
                </Badge>
              </TableCell>
              <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                {canManage && entry.adoption_state === "pending" ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={adoptPending}
                    onClick={() => onAdopt(entry)}
                  >
                    {t(($) => $.knowledge.adopt)}
                  </Button>
                ) : null}
              </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      {!canManage ? (
        <p className="border-t px-3 py-2 text-caption text-muted-foreground">
          {t(($) => $.knowledge.ownerOnly)}
        </p>
      ) : null}
    </div>
  );
}

type UseKnowledgeDirs = UseQueryResult<KnowledgeDir[], Error>;
type UseKnowledgeEntries = UseQueryResult<KnowledgeEntry[], Error>;

function DirsSegment({
  dirs,
  dirsQuery,
  external = false,
  kindLabel,
  healthLabel,
  scanResultLabel,
  canManage,
  scanPending,
  unregisterPending,
  onOpenDir,
  onScan,
  onUnregister,
  onJumpToDirs,
}: {
  dirs: KnowledgeDir[];
  dirsQuery: UseKnowledgeDirs;
  external?: boolean;
  kindLabel: (kind: string) => string;
  healthLabel: (state: string) => string;
  scanResultLabel: (result: string) => string;
  canManage: boolean;
  scanPending: boolean;
  unregisterPending: boolean;
  onOpenDir: (dir: KnowledgeDir) => void;
  onScan: (dir: KnowledgeDir) => void;
  onUnregister: (dir: KnowledgeDir) => void;
  onJumpToDirs?: () => void;
}) {
  const { t } = useT("self-evolution");
  if (dirsQuery.isPending) return <Skeleton className="h-40 w-full" />;
  if (dirsQuery.isError) {
    return (
      <p role="alert" className="text-body text-destructive">
        {t(($) => $.knowledge.error.read)}
        <Button size="sm" variant="outline" className="ml-2" onClick={() => void dirsQuery.refetch()}>
          {t(($) => $.knowledge.error.retry)}
        </Button>
      </p>
    );
  }
  if (!dirs.length) {
    return external ? (
      <p className="text-body text-muted-foreground">{t(($) => $.knowledgeSeg.emptyExternal)}</p>
    ) : (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>{t(($) => $.knowledge.emptyDirs.title)}</EmptyTitle>
          <EmptyDescription>{t(($) => $.knowledge.emptyDirs.description)}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  return (
    <div className="overflow-hidden rounded-lg border" data-testid={external ? "knowledge-external-table" : "knowledge-dirs-table"}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[20%]">{t(($) => $.knowledgeSeg.colSource)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.colPath)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.attrKind)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.colHealth)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.attrEntries)}</TableHead>
            <TableHead>{t(($) => $.knowledgeSeg.colLastScan)}</TableHead>
            {canManage ? (
              <TableHead className="w-[10rem] text-right">{t(($) => $.knowledgeSeg.colActions)}</TableHead>
            ) : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {dirs.map((dir) => (
            <TableRow
              key={dir.id}
              className="cursor-pointer"
              data-testid={`knowledge-dir-${dir.id}`}
              onClick={() => onOpenDir(dir)}
            >
              <TableCell className="max-w-0 truncate text-body font-medium">{dir.label}</TableCell>
              <TableCell className="max-w-0 truncate font-mono text-caption text-muted-foreground">
                {dir.path}
              </TableCell>
              <TableCell>
                <Badge variant="outline">{kindLabel(dir.kind)}</Badge>
              </TableCell>
              <TableCell>
                <span className="flex items-center gap-1.5 text-caption">
                  <span
                    aria-hidden
                    className={`inline-block size-1.5 rounded-full ${dir.health_state === "ok" ? "bg-emerald-500" : "bg-destructive"}`}
                  />
                  {healthLabel(dir.health_state)}
                </span>
                {dir.health_state !== "ok" && dir.health_note ? (
                  <span className="block max-w-56 truncate text-caption text-destructive">
                    {dir.health_note}
                  </span>
                ) : null}
              </TableCell>
              <TableCell className="text-caption">{dir.entry_count}</TableCell>
              <TableCell className="max-w-0 truncate text-caption text-muted-foreground">
                {dir.last_scan
                  ? `${scanResultLabel(dir.last_scan.result)} · ${new Date(dir.last_scan.started_at).toLocaleDateString()}`
                  : t(($) => $.knowledge.neverScanned)}
              </TableCell>
              {canManage ? (
                <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                  {dir.kind !== "ultimate" ? (
                    <div className="flex justify-end gap-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={scanPending}
                        onClick={() => onScan(dir)}
                      >
                        {t(($) => $.knowledge.scan)}
                      </Button>
                      <UnregisterButton
                        pending={unregisterPending}
                        label={t(($) => $.knowledge.unregister)}
                        confirmLabel={t(($) => $.knowledge.unregister)}
                        cancelLabel={t(($) => $.skills.cancel)}
                        title={t(($) => $.knowledge.unregister)}
                        description={t(($) => $.knowledgeSeg.unregisterConfirm, { label: dir.label })}
                        onConfirm={() => onUnregister(dir)}
                      />
                    </div>
                  ) : null}
                </TableCell>
              ) : null}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {external && onJumpToDirs ? (
        <div className="border-t px-3 py-2">
          <button type="button" className={buttonVariants({ variant: "link", size: "sm" })} onClick={onJumpToDirs}>
            {t(($) => $.knowledgeSeg.jumpToDirs)}
          </button>
        </div>
      ) : null}
    </div>
  );
}

function UnregisterButton({
  label,
  title,
  description,
  confirmLabel,
  cancelLabel,
  pending,
  onConfirm,
}: {
  label: string;
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel: string;
  pending: boolean;
  onConfirm: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        size="sm"
        variant="ghost"
        className="text-destructive hover:text-destructive"
        disabled={pending}
        onClick={() => setOpen(true)}
      >
        {label}
      </Button>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{title}</AlertDialogTitle>
            <AlertDialogDescription>{description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{cancelLabel}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => {
                setOpen(false);
                onConfirm();
              }}
            >
              {confirmLabel}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

/**
 * The page-level "add external source" dialog (walk #3): the CTA moved from
 * a collapsed bottom ghost to the page header. Only the two fields a human
 * can meaningfully provide; the source kind is the server's decision —
 * manual paths always land as candidate_cli (RUYI-289).
 */
function RegisterSourceDialog({
  open,
  pending,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (draft: { kind: string; path: string; label?: string }) => void;
}) {
  const { t } = useT("self-evolution");
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");

  const submit = () => {
    onSubmit({
      kind: "candidate_cli",
      path,
      ...(label.trim() ? { label: label.trim() } : {}),
    });
    setPath("");
    setLabel("");
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="knowledge-register-dialog">
        <DialogHeader>
          <DialogTitle>{t(($) => $.knowledgeSeg.addSource)}</DialogTitle>
          <DialogDescription>{t(($) => $.knowledge.advancedDescription)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="knowledge-register-path">{t(($) => $.knowledge.pathLabel)}</Label>
            <Input
              id="knowledge-register-path"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              // Example filesystem path of a memory mirror, not copy.
              // eslint-disable-next-line no-restricted-syntax
              placeholder="/srv/memories/project-x"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="knowledge-register-label">{t(($) => $.knowledge.labelLabel)}</Label>
            <Input
              id="knowledge-register-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder={t(($) => $.knowledge.labelPlaceholder)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button size="sm" disabled={pending || path.trim() === ""} onClick={submit}>
            {t(($) => $.knowledge.registerSubmit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
