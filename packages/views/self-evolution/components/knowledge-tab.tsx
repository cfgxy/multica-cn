"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
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
 * The knowledge library (RUYI-265 §K, reworked by RUYI-289).
 *
 * Auto-discovered sources lead the view; manually adding an external source is
 * a collapsed advanced action. Scanning never runs in the UI — registration
 * and scan requests only queue work that the hosting daemon picks up, and the
 * queued state is shown as a badge instead of being pretended away. The single
 * write path is adoption, which queues a background transfer into the
 * knowledge hub. All of register/scan/unregister/adopt is owner-only on the
 * server, so the controls are hidden rather than disabled for non-owners.
 */
export function KnowledgeTab({ wsId }: { wsId: string }) {
  const { t } = useT("self-evolution");
  const currentMember = useCurrentMember(wsId);
  const canManage = currentMember.role === "owner";

  const [dirId, setDirId] = useState("");
  const [query, setQuery] = useState("");

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

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1.5">
        <h2 className="text-title font-semibold">{t(($) => $.knowledge.title)}</h2>
        <p className="text-body text-muted-foreground">{t(($) => $.knowledge.description)}</p>
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
                  {dir.scan_requested ? (
                    <Badge variant="outline">{t(($) => $.knowledge.scanRequested)}</Badge>
                  ) : null}
                  <span className="ml-auto text-caption text-muted-foreground">
                    {t(($) => $.knowledge.entryCount, { n: dir.entry_count })}
                  </span>
                </span>
                <p className="truncate font-mono text-caption text-muted-foreground">{dir.path}</p>
                {dir.health_note ? (
                  <p className="text-caption text-muted-foreground">{dir.health_note}</p>
                ) : null}
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

      {canManage ? (
        <AdvancedSourceForm
          pending={register.isPending}
          onSubmit={(draft) => {
            register.mutate(draft, { onError: mutationError });
          }}
        />
      ) : null}
    </div>
  );
}

/**
 * Manual registration is an advanced action: auto-discovery is the default
 * path, so the form hides behind a collapsed section and only asks for the
 * two fields a human can meaningfully provide. The source kind is the
 * server's decision — manual paths always land as candidate_cli (RUYI-289).
 */
function AdvancedSourceForm({
  pending,
  onSubmit,
}: {
  pending: boolean;
  onSubmit: (draft: { kind: string; path: string; label?: string }) => void;
}) {
  const { t } = useT("self-evolution");
  const [open, setOpen] = useState(false);
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
    <section className="space-y-3 border-t pt-4" data-testid="knowledge-advanced">
      <Button
        size="sm"
        variant="ghost"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        {open ? "▾" : "▸"} {t(($) => $.knowledge.advancedTitle)}
      </Button>
      {open ? (
        <div className="space-y-3 rounded-md border border-border p-3">
          <p className="text-caption text-muted-foreground">
            {t(($) => $.knowledge.advancedDescription)}
          </p>
          <div className="grid gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
            <div className="space-y-1.5">
              <Label htmlFor="knowledge-advanced-path">{t(($) => $.knowledge.pathLabel)}</Label>
              <Input
                id="knowledge-advanced-path"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                // Example filesystem path of a memory mirror, not copy.
                // eslint-disable-next-line no-restricted-syntax
                placeholder="/srv/memories/project-x"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="knowledge-advanced-label">{t(($) => $.knowledge.labelLabel)}</Label>
              <Input
                id="knowledge-advanced-label"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder={t(($) => $.knowledge.labelPlaceholder)}
              />
            </div>
          </div>
          <Button size="sm" disabled={pending || path.trim() === ""} onClick={submit}>
            {t(($) => $.knowledge.registerSubmit)}
          </Button>
        </div>
      ) : null}
    </section>
  );
}
