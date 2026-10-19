import { useCallback, useEffect, useMemo, useState } from "react";
import { Archive, HardDrive, Loader2, RefreshCw, Trash2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
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
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { SettingsTab } from "@multica/views/settings";
import { useT } from "@multica/views/i18n";
import { toast } from "sonner";
import type {
  EnvironmentArchive,
  EnvironmentArchiveResult,
  EnvironmentCachePreview,
  ManagedEnvironment,
} from "../../../shared/environment-manager";

type View = "environments" | "cache" | "archives";

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let size = value / 1024;
  let unit = units[0];
  for (let index = 1; size >= 1024 && index < units.length; index += 1) {
    size /= 1024;
    unit = units[index];
  }
  return `${size.toFixed(1)} ${unit}`;
}

function EnvironmentRow({
  row,
  selected,
  onSelectedChange,
}: {
  row: ManagedEnvironment;
  selected: boolean;
  onSelectedChange: (checked: boolean) => void;
}) {
  const { t } = useT("settings");
  const protectedRow = row.active || row.protectionReason !== "";
  return (
    <div className="grid grid-cols-[28px_minmax(0,1fr)_100px_100px] items-center gap-3 border-b py-2.5 last:border-b-0">
      <Checkbox
        checked={selected}
        disabled={protectedRow}
        onCheckedChange={(checked) => onSelectedChange(checked === true)}
        aria-label={row.taskShort || row.taskId}
      />
      <div className="min-w-0">
        <div className="truncate text-body font-medium">
          {row.taskShort || row.issueId || row.taskId.slice(0, 12)}
        </div>
        <div className="truncate text-caption text-muted-foreground">
          {[row.workspaceShort, row.parentStatus, row.protectionReason || row.retentionReason]
            .filter(Boolean)
            .join(" · ") || t(($) => $.desktop.environment.unknown_status)}
        </div>
      </div>
      <span className="text-right text-caption tabular-nums text-muted-foreground">
        {formatBytes(row.sizeBytes)}
      </span>
      <span className="text-right text-caption text-muted-foreground">
        {protectedRow
          ? t(($) => $.desktop.environment.protected)
          : t(($) => $.desktop.environment.available)}
      </span>
    </div>
  );
}

export function EnvironmentManagerTab() {
  const { t } = useT("settings");
  const [view, setView] = useState<View>("environments");
  const [environments, setEnvironments] = useState<ManagedEnvironment[]>([]);
  const [cachePreview, setCachePreview] = useState<EnvironmentCachePreview[]>([]);
  const [archivePreview, setArchivePreview] = useState<EnvironmentArchiveResult[]>([]);
  const [archives, setArchives] = useState<EnvironmentArchive[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<"cache" | "archive" | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [inventory, savedArchives] = await Promise.all([
        window.daemonAPI.environments.inventory(),
        window.daemonAPI.environments.archives(),
      ]);
      setEnvironments(inventory);
      setArchives(savedArchives);
      setSelected((current) => current.filter((id) => inventory.some((row) => row.environmentId === id && !row.active && !row.protectionReason)));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t(($) => $.desktop.environment.load_failed));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => { void refresh(); }, [refresh]);

  const selectedIds = useMemo(() => new Set(selected), [selected]);
  const selectedEnvironmentIds = useMemo(
    () => environments.filter((row) => selectedIds.has(row.environmentId)).map((row) => row.environmentId),
    [environments, selectedIds],
  );

  const toggle = (id: string, checked: boolean) => {
    setSelected((current) => checked
      ? current.includes(id) ? current : [...current, id]
      : current.filter((item) => item !== id));
  };

  const previewCache = async () => {
    setBusy(true);
    try {
      const rows = await window.daemonAPI.environments.previewCache();
      setCachePreview(rows.filter((row) => selectedEnvironmentIds.includes(row.environmentId)));
      setView("cache");
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t(($) => $.desktop.environment.operation_failed));
    } finally {
      setBusy(false);
    }
  };

  const cleanCache = async () => {
    const rows = cachePreview.filter((row) => row.candidates > 0 && !row.reason && selectedIds.has(row.environmentId));
    if (rows.length === 0) return;
    setBusy(true);
    try {
      const results = await window.daemonAPI.environments.cleanCache(rows.map(({ environmentId, revision }) => ({ environmentId, revision })));
      const reclaimed = results.reduce((sum, row) => sum + row.removedBytes, 0);
      toast.success(t(($) => $.desktop.environment.cache_cleaned, { size: formatBytes(reclaimed) }));
      setCachePreview([]);
      await refresh();
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t(($) => $.desktop.environment.operation_failed));
    } finally {
      setBusy(false);
    }
  };

  const previewArchives = async () => {
    setBusy(true);
    try {
      const rows = await window.daemonAPI.environments.previewArchive();
      setArchivePreview(rows.filter((row) => selectedEnvironmentIds.includes(row.environmentId)));
      setView("environments");
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t(($) => $.desktop.environment.operation_failed));
    } finally {
      setBusy(false);
    }
  };

  const archiveSelected = async () => {
    const rows = archivePreview.filter((row) => row.revision && !row.reason && selectedIds.has(row.environmentId));
    if (rows.length === 0) return;
    setBusy(true);
    try {
      const results = await window.daemonAPI.environments.archive(rows.map(({ environmentId, revision }) => ({ environmentId, revision })));
      const reclaimed = results.filter((row) => row.reclaimed).length;
      toast.success(t(($) => $.desktop.environment.archived, { count: reclaimed }));
      setArchivePreview([]);
      setSelected([]);
      setView("archives");
      await refresh();
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t(($) => $.desktop.environment.operation_failed));
    } finally {
      setBusy(false);
    }
  };

  const restore = async (archiveId: string) => {
    setBusy(true);
    try {
      const [result] = await window.daemonAPI.environments.restoreArchive(archiveId);
      if (result?.restored) {
        toast.success(t(($) => $.desktop.environment.restored));
        await refresh();
      } else {
        toast.error(result?.reason || t(($) => $.desktop.environment.restore_failed));
      }
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t(($) => $.desktop.environment.restore_failed));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
    <SettingsTab
      title={t(($) => $.desktop.environment.title)}
      description={t(($) => $.desktop.environment.description)}
    >
      <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
        <Tabs value={view} onValueChange={(value) => setView(value as View)}>
          <TabsList>
            <TabsTrigger value="environments">{t(($) => $.desktop.environment.environments)}</TabsTrigger>
            <TabsTrigger value="cache"><HardDrive className="mr-1.5 size-3.5" />{t(($) => $.desktop.environment.cache)}</TabsTrigger>
            <TabsTrigger value="archives"><Archive className="mr-1.5 size-3.5" />{t(($) => $.desktop.environment.archives)}{archives.length > 0 ? ` (${archives.length})` : ""}</TabsTrigger>
          </TabsList>
        </Tabs>
        <Button variant="outline" size="icon" aria-label={t(($) => $.desktop.environment.refresh)} onClick={() => void refresh()} disabled={loading || busy}>
          {loading ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
        </Button>
      </div>

      {error && <p role="alert" className="mt-4 text-body text-destructive">{error}</p>}

      {view === "environments" && (
        <section className="mt-4">
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
            <p className="text-caption text-muted-foreground">{t(($) => $.desktop.environment.selected, { count: selected.length })}</p>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => void previewCache()} disabled={busy || selected.length === 0}>
                <HardDrive className="mr-1.5 size-3.5" />{t(($) => $.desktop.environment.preview_cache)}
              </Button>
              <Button size="sm" variant="outline" onClick={() => void previewArchives()} disabled={busy || selected.length === 0}>
                <Archive className="mr-1.5 size-3.5" />{t(($) => $.desktop.environment.preview_archive)}
              </Button>
            </div>
          </div>
          <div className="grid grid-cols-[28px_minmax(0,1fr)_100px_100px] gap-3 border-b py-2 text-caption font-medium text-muted-foreground">
            <span />
            <span>{t(($) => $.desktop.environment.environment)}</span>
            <span className="text-right">{t(($) => $.desktop.environment.size)}</span>
            <span className="text-right">{t(($) => $.desktop.environment.state)}</span>
          </div>
          {loading ? <div className="flex justify-center py-10"><Loader2 className="size-4 animate-spin" /></div> : environments.length === 0 ? (
            <p className="py-10 text-center text-body text-muted-foreground">{t(($) => $.desktop.environment.empty)}</p>
          ) : environments.map((row) => (
            <EnvironmentRow key={row.environmentId} row={row} selected={selectedIds.has(row.environmentId)} onSelectedChange={(checked) => toggle(row.environmentId, checked)} />
          ))}
          {archivePreview.length > 0 && (
            <div className="mt-3 flex items-center justify-between gap-3 border-t pt-3">
              <span className="text-caption text-muted-foreground">{t(($) => $.desktop.environment.archive_preview_count, { count: archivePreview.length })}</span>
              <Button size="sm" onClick={() => setConfirmAction("archive")} disabled={busy || !archivePreview.some((row) => selectedIds.has(row.environmentId) && !row.reason)}>
                {busy && <Loader2 className="mr-1.5 size-3.5 animate-spin" />}{t(($) => $.desktop.environment.archive_now)}
              </Button>
            </div>
          )}
        </section>
      )}

      {view === "cache" && (
        <section className="mt-4">
          <div className="mb-2 flex items-center justify-between gap-3">
            <p className="text-caption text-muted-foreground">{t(($) => $.desktop.environment.cache_preview_count, { count: cachePreview.length })}</p>
            <Button size="sm" variant="destructive" onClick={() => setConfirmAction("cache")} disabled={busy || !cachePreview.some((row) => row.candidates > 0 && !row.reason)}>
              {busy ? <Loader2 className="mr-1.5 size-3.5 animate-spin" /> : <Trash2 className="mr-1.5 size-3.5" />}{t(($) => $.desktop.environment.clean_cache)}
            </Button>
          </div>
          {cachePreview.map((row) => (
            <div key={row.environmentId} className="flex items-center justify-between gap-3 border-b py-2.5 last:border-b-0">
              <span className="min-w-0 truncate font-mono text-caption">{row.environmentId.slice(0, 12)}</span>
              <span className="shrink-0 text-caption tabular-nums">{formatBytes(row.sizeBytes)} · {row.candidates}</span>
              {row.reason && <span className="text-caption text-muted-foreground">{row.reason}</span>}
            </div>
          ))}
          {cachePreview.length === 0 && <p className="py-8 text-center text-body text-muted-foreground">{t(($) => $.desktop.environment.no_preview)}</p>}
        </section>
      )}

      {view === "archives" && (
        <section className="mt-4">
          {archives.map((row) => (
            <div key={row.archiveId} className="grid grid-cols-[minmax(0,1fr)_100px_auto] items-center gap-3 border-b py-2.5 last:border-b-0">
              <div className="min-w-0">
                <div className="truncate text-body font-medium">{row.taskName || row.taskId.slice(0, 12)}</div>
                <div className="truncate text-caption text-muted-foreground">{new Date(row.createdAt).toLocaleString()} · {row.kind}</div>
              </div>
              <span className="text-right text-caption tabular-nums">{formatBytes(row.archiveBytes)}</span>
              <Button size="sm" variant="outline" onClick={() => void restore(row.archiveId)} disabled={busy}>
                {busy ? <Loader2 className="mr-1.5 size-3.5 animate-spin" /> : <RefreshCw className="mr-1.5 size-3.5" />}{t(($) => $.desktop.environment.restore)}
              </Button>
            </div>
          ))}
          {archives.length === 0 && <p className="py-10 text-center text-body text-muted-foreground">{t(($) => $.desktop.environment.no_archives)}</p>}
        </section>
      )}
    </SettingsTab>
    <AlertDialog open={confirmAction !== null} onOpenChange={(open) => !open && setConfirmAction(null)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {confirmAction === "cache"
              ? t(($) => $.desktop.environment.confirm_cache_title)
              : t(($) => $.desktop.environment.confirm_archive_title)}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {confirmAction === "cache"
              ? t(($) => $.desktop.environment.confirm_cache_description)
              : t(($) => $.desktop.environment.confirm_archive_description)}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t(($) => $.desktop.environment.cancel)}</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              if (confirmAction === "cache") void cleanCache();
              if (confirmAction === "archive") void archiveSelected();
              setConfirmAction(null);
            }}
          >
            {t(($) => $.desktop.environment.confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  );
}
