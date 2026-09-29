"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
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
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { clientErrorMessage } from "@multica/core/api";
import { useCurrentMember } from "@multica/core/permissions";
import {
  knowledgeDirsOptions,
  knowledgeEntriesOptions,
  useAdoptKnowledgeEntry,
  useRegisterKnowledgeDir,
  useScanKnowledgeDir,
  useUnregisterKnowledgeDir,
} from "@multica/core/self-evolution";
import { useT } from "../../i18n";

/**
 * The knowledge mirror (RUYI-265 §K).
 *
 * Directories are scanned strictly read-only; the one write path is adoption,
 * which transfers an entry's content into the ultimate library. That transfer
 * is owner-only on the server — registering, scanning and unregistering a
 * directory too — so the buttons are hidden rather than disabled for
 * non-owners. Everyone can read the mirror and the adoption trail.
 */
export function KnowledgeTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const [dirId, setDirId] = useState("");
  const [query, setQuery] = useState("");
  const [registerOpen, setRegisterOpen] = useState(false);

  const dirs = useQuery(knowledgeDirsOptions(wsId));
  const entries = useQuery(knowledgeEntriesOptions(wsId, dirId, query));
  const register = useRegisterKnowledgeDir(wsId);
  const scan = useScanKnowledgeDir(wsId);
  const unregister = useUnregisterKnowledgeDir(wsId);
  const adopt = useAdoptKnowledgeEntry(wsId);

  const mutationError = (e: unknown) =>
    toast.error(clientErrorMessage(e) ?? t(($) => $.knowledge.error.write));

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

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-col gap-1.5">
          <h2 className="text-title font-semibold">{t(($) => $.knowledge.title)}</h2>
          <p className="text-body text-muted-foreground">{t(($) => $.knowledge.description)}</p>
        </div>
        {canManage ? (
          <Button size="sm" variant="outline" onClick={() => setRegisterOpen(true)}>
            {t(($) => $.knowledge.register)}
          </Button>
        ) : null}
      </div>

      <section className="min-w-0 space-y-3" aria-label={t(($) => $.knowledge.dirsTitle)}>
        <h3 className="text-title font-medium">{t(($) => $.knowledge.dirsTitle)}</h3>
        {dirs.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : dirs.isError ? (
          <p role="alert" className="text-body text-destructive">
            {t(($) => $.knowledge.error.read)}
            <Button size="sm" variant="outline" className="ml-2" onClick={() => void dirs.refetch()}>
              {t(($) => $.knowledge.error.retry)}
            </Button>
          </p>
        ) : !dirs.data?.length ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{t(($) => $.knowledge.emptyDirs.title)}</EmptyTitle>
              <EmptyDescription>{t(($) => $.knowledge.emptyDirs.description)}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ul className="flex flex-col gap-2">
            {dirs.data.map((dir) => (
              <li
                key={dir.id}
                className="space-y-1.5 rounded-md border border-border px-3 py-2"
                data-testid={`knowledge-dir-${dir.id}`}
              >
                <span className="flex flex-wrap items-center gap-2">
                  <span className="min-w-0 truncate text-body font-medium">{dir.label}</span>
                  <Badge variant="outline">{kindLabel(dir.kind)}</Badge>
                  {dir.kind === "ultimate" ? null : (
                    <Badge variant={dir.health_state === "ok" ? "secondary" : "outline"}>
                      {dir.health_state === "ok"
                        ? t(($) => $.knowledge.health.ok)
                        : t(($) => $.knowledge.health.unhealthy)}
                    </Badge>
                  )}
                  <span className="ml-auto text-caption text-muted-foreground">
                    {t(($) => $.knowledge.entryCount, { n: dir.entry_count })}
                  </span>
                </span>
                <p className="truncate font-mono text-caption text-muted-foreground">{dir.path}</p>
                <p className="text-caption text-muted-foreground">
                  {dir.last_scan
                    ? t(($) => $.knowledge.lastScan, {
                        result: scanResultLabel(dir.last_scan.result),
                        added: dir.last_scan.added,
                        updated: dir.last_scan.updated,
                        removed: dir.last_scan.removed,
                      })
                    : t(($) => $.knowledge.neverScanned)}
                </p>
                {canManage && dir.kind !== "ultimate" ? (
                  <div className="flex gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={scan.isPending}
                      onClick={() =>
                        scan.mutate(dir.id, { onError: mutationError })
                      }
                    >
                      {t(($) => $.knowledge.scan)}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={unregister.isPending}
                      onClick={() =>
                        unregister.mutate(dir.id, { onError: mutationError })
                      }
                    >
                      {t(($) => $.knowledge.unregister)}
                    </Button>
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="min-w-0 space-y-3" aria-label={t(($) => $.knowledge.entriesTitle)}>
        <div className="flex flex-wrap items-end justify-between gap-3">
          <h3 className="text-title font-medium">{t(($) => $.knowledge.entriesTitle)}</h3>
          <div className="flex items-center gap-2">
            <Select
              items={[
                { value: "", label: t(($) => $.knowledge.allDirs) },
                ...(dirs.data ?? []).map((d) => ({ value: d.id, label: d.label })),
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
                    : dirs.data?.find((d) => d.id === dirId)?.label ?? t(($) => $.knowledge.allDirs)}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">{t(($) => $.knowledge.allDirs)}</SelectItem>
                {dirs.data?.map((d) => (
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
          </div>
        </div>
        {entries.isPending ? (
          <Skeleton className="h-32 w-full" />
        ) : entries.isError ? (
          <p role="alert" className="text-body text-destructive">
            {t(($) => $.knowledge.error.read)}
            <Button
              size="sm"
              variant="outline"
              className="ml-2"
              onClick={() => void entries.refetch()}
            >
              {t(($) => $.knowledge.error.retry)}
            </Button>
          </p>
        ) : !entries.data?.length ? (
          <p className="text-body text-muted-foreground">{t(($) => $.knowledge.emptyEntries)}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {entries.data.map((entry) => (
              <li
                key={entry.id}
                className="space-y-1 rounded-md border border-border px-3 py-2"
                data-testid={`knowledge-entry-${entry.key}`}
              >
                <span className="flex flex-wrap items-center gap-2">
                  <span className="min-w-0 truncate font-mono text-caption font-medium">
                    {entry.key}
                  </span>
                  <Badge variant="outline">{mirrorLabel(entry.mirror_state)}</Badge>
                  <Badge
                    variant={entry.adoption_state === "adopted" ? "secondary" : "outline"}
                  >
                    {adoptionLabel(entry.adoption_state)}
                  </Badge>
                  {canManage && entry.adoption_state === "pending" ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="ml-auto"
                      disabled={adopt.isPending}
                      onClick={() =>
                        adopt.mutate(entry.id, { onError: mutationError })
                      }
                    >
                      {t(($) => $.knowledge.adopt)}
                    </Button>
                  ) : null}
                </span>
                <p className="line-clamp-2 whitespace-pre-wrap text-caption text-muted-foreground">
                  {entry.content}
                </p>
                {entry.adoption_state === "adopted" && entry.adopted_from_key ? (
                  <p className="text-caption text-muted-foreground">
                    {t(($) => $.knowledge.adoptedFrom, { key: entry.adopted_from_key })}
                  </p>
                ) : null}
                {entry.adoption_error ? (
                  <p role="alert" className="text-caption text-destructive">
                    {entry.adoption_error}
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {!canManage ? (
          <p className="text-caption text-muted-foreground">{t(($) => $.knowledge.ownerOnly)}</p>
        ) : null}
      </section>

      <RegisterDirDialog
        open={registerOpen}
        pending={register.isPending}
        onOpenChange={setRegisterOpen}
        onSubmit={(draft) => {
          register.mutate(draft, {
            onSuccess: () => setRegisterOpen(false),
            onError: mutationError,
          });
        }}
      />
    </div>
  );
}

const DIR_KINDS = ["candidate_cli", "candidate_auto"] as const;

/**
 * The ultimate library is designated elsewhere (it is the adoption target, not
 * one of the candidates this form registers), so only candidate kinds are
 * offered here.
 */
function RegisterDirDialog({
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
  const [kind, setKind] = useState<(typeof DIR_KINDS)[number]>("candidate_cli");
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");

  const submit = () => {
    onSubmit({
      kind,
      path,
      ...(label.trim() ? { label: label.trim() } : {}),
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t(($) => $.knowledge.registerTitle)}</DialogTitle>
          <DialogDescription>{t(($) => $.knowledge.registerDescription)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>{t(($) => $.knowledge.kindLabel)}</Label>
            <Select
              items={DIR_KINDS.map((v) => ({
                value: v,
                label: t(($) => $.knowledge.kind[v]),
              }))}
              value={kind}
              onValueChange={(next) => {
                if (typeof next === "string") setKind(next as (typeof DIR_KINDS)[number]);
              }}
            >
              <SelectTrigger size="sm" className="w-full">
                <SelectValue>{t(($) => $.knowledge.kind[kind])}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                {DIR_KINDS.map((v) => (
                  <SelectItem key={v} value={v}>
                    {t(($) => $.knowledge.kind[v])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="knowledge-path">{t(($) => $.knowledge.pathLabel)}</Label>
            <Input
              id="knowledge-path"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              // Example filesystem path of a memory mirror, not copy.
              // eslint-disable-next-line no-restricted-syntax
              placeholder="/srv/memories/project-x"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="knowledge-label">{t(($) => $.knowledge.labelLabel)}</Label>
            <Input
              id="knowledge-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder={t(($) => $.knowledge.labelPlaceholder)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t(($) => $.knowledge.cancel)}
          </Button>
          <Button disabled={pending || path.trim() === ""} onClick={submit}>
            {t(($) => $.knowledge.registerSubmit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
