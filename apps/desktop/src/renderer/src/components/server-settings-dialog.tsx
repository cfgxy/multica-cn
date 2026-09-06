"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown, ChevronRight, Loader2, Plus, SquarePen, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "@multica/views/i18n";
import { useAuthStore } from "@multica/core/auth";
import {
  SERVER_PROBE_PATH,
  findDuplicateServer,
  interpretProbeResponse,
  isPlainHttp,
  isValidServerUrl,
  normalizeUrl,
  type ServerEntry,
} from "@multica/core/servers";
import { useServerStore } from "../platform/desktop-servers";
import { resetActiveServerSessionStorage } from "../platform/desktop-servers";
import { useServerSwitcherStore } from "../stores/server-switcher-store";

/** Same probe timeout as the accepted mobile implementation. */
const PROBE_TIMEOUT_MS = 8_000;

type ProbeState = "idle" | "probing" | "ok" | "failed";

/**
 * 【管理服务器】dialog (RUYI-59) — CRUD over the configured multica
 * instances, mirroring the accepted mobile server settings: the built-in
 * entry exposes no edit/delete affordances, the active entry is not
 * deletable, deleting confirms first, and saving an address change on the
 * ACTIVE server signs the user out (the live token was minted by the old
 * address) before the reload boots into the new URL.
 *
 * Any change to the active server's api url reloads the window — the API
 * client is a boot-time singleton, so the new address takes effect exactly
 * like a server switch does.
 */
export function ServerSettingsDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useT("settings");
  const servers = useServerStore((s) => s.servers);
  const activeServerId = useServerStore((s) => s.activeServerId);
  // The switcher store is the single owner of which screen (list vs form)
  // is visible — the menu group and this dialog both write it there.
  const editingServerId = useServerSwitcherStore((s) => s.editingServerId);
  const openManage = useServerSwitcherStore((s) => s.openManage);
  // editingServerId "new" = create form; an id = edit form; null = list.
  const [probe, setProbe] = useState<ProbeState>("idle");
  const [name, setName] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [webUrl, setWebUrl] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<ServerEntry | null>(null);
  const [confirmActiveUrlChange, setConfirmActiveUrlChange] = useState(false);

  const editing = useMemo(() => {
    if (!editingServerId || editingServerId === "new") return null;
    return servers.find((s) => s.id === editingServerId) ?? null;
  }, [editingServerId, servers]);
  const isNew = editingServerId === "new";
  const formOpen = isNew || editing != null;

  // Form fields are derived from the editing target whenever the target
  // changes — the dialog can mount straight into form mode (host renders
  // it from the switcher store), so call-order coupling is not allowed.
  const openFormTargetId = isNew ? "new" : (editing?.id ?? null);
  const lastTargetRef = useRef<string | null>(null);
  useEffect(() => {
    if (!formOpen) {
      lastTargetRef.current = null;
      return;
    }
    if (lastTargetRef.current === openFormTargetId) return;
    lastTargetRef.current = openFormTargetId;
    setProbe("idle");
    setName(editing?.name ?? "");
    setApiUrl(editing?.apiUrl ?? "");
    setWebUrl(editing?.webUrl ?? "");
    // An explicitly set web address starts expanded so it is never lost.
    setAdvancedOpen(!!editing?.webUrl);
  }, [formOpen, openFormTargetId, editing]);

  const openForm = (entry: ServerEntry | "new") => {
    openManage(entry === "new" ? "new" : entry.id);
  };

  const apiTouched = apiUrl.trim().length > 0;
  const apiValid = isValidServerUrl(apiUrl);
  const duplicate = apiValid
    ? findDuplicateServer(servers, apiUrl, editing?.id)
    : undefined;
  const webTouched = webUrl.trim().length > 0;
  const webValid = !webTouched || isValidServerUrl(webUrl);

  const apiError = !apiTouched
    ? null
    : !apiValid
      ? t(($) => $.server.form.invalid_url)
      : duplicate
        ? t(($) => $.server.form.duplicate)
        : null;
  const webError =
    webTouched && !webValid ? t(($) => $.server.form.invalid_url) : null;
  const canSave = apiValid && !duplicate && webValid && !saving;

  const onProbe = async () => {
    if (!apiValid) return;
    setProbe("probing");
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
    try {
      const res = await fetch(`${normalizeUrl(apiUrl)}${SERVER_PROBE_PATH}`, {
        signal: controller.signal,
      });
      setProbe(
        interpretProbeResponse(res.status, res.headers.get("content-type"))
          ? "ok"
          : "failed",
      );
    } catch {
      setProbe("failed");
    } finally {
      clearTimeout(timer);
    }
  };

  /** Persist; returns false when the write failed (user stays put). */
  const persist = (): boolean => {
    try {
      const input = {
        name,
        apiUrl,
        webUrl: webTouched ? webUrl : null,
      };
      const store = useServerStore.getState();
      if (isNew) store.addServer(input);
      else if (editing) store.updateServer(editing.id, input);
      return true;
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : t(($) => $.server.form.save_failed_message),
      );
      return false;
    }
  };

  const onSaveConfirmed = () => {
    setConfirmActiveUrlChange(false);
    setSaving(true);
    const saved = persist();
    setSaving(false);
    if (!saved) return;
    const switchesActiveServer =
      !!editing &&
      editing.id === activeServerId &&
      normalizeUrl(apiUrl) !== editing.apiUrl;
    if (switchesActiveServer) {
      // The live token belongs to the old address — reset the session
      // (mobile parity) and reload into the login page of the new URL.
      resetActiveServerSessionStorage();
      window.location.reload();
      return;
    }
    if (editing?.id === activeServerId) {
      // A non-address field (name/webUrl) changed on the active server —
      // reload so the boot-time config picks it up.
      window.location.reload();
      return;
    }
    onClose();
  };

  const onSaveClick = () => {
    if (!canSave) return;
    const switchesActiveServer =
      !!editing &&
      editing.id === activeServerId &&
      normalizeUrl(apiUrl) !== editing.apiUrl;
    if (switchesActiveServer && useAuthStore.getState().user) {
      setConfirmActiveUrlChange(true);
      return;
    }
    onSaveConfirmed();
  };

  const onDeleteConfirmed = () => {
    if (!deleteTarget) return;
    try {
      useServerStore.getState().removeServer(deleteTarget.id);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : undefined);
    }
    setDeleteTarget(null);
  };

  const customServers = servers.filter((s) => !s.builtIn);

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next) onClose();
        }}
      >
        <DialogContent className="max-w-lg" data-testid="server-settings-dialog">
          <DialogHeader>
            <DialogTitle>{t(($) => $.server.manage_title)}</DialogTitle>
            <DialogDescription className="text-caption">
              {t(($) => $.server.hint)}
            </DialogDescription>
          </DialogHeader>

          {formOpen ? (
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="server-name" className="text-caption text-muted-foreground">
                  {t(($) => $.server.form.name_label)}
                </Label>
                <Input
                  id="server-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={t(($) => $.server.form.name_placeholder)}
                  autoCapitalize="none"
                  autoCorrect="off"
                />
              </div>

              <div className="flex flex-col gap-1.5">
                <Label htmlFor="server-api-url" className="text-caption text-muted-foreground">
                  {t(($) => $.server.form.api_label)}
                </Label>
                <Input
                  id="server-api-url"
                  value={apiUrl}
                  onChange={(e) => {
                    setApiUrl(e.target.value);
                    setProbe("idle");
                  }}
                  placeholder={t(($) => $.server.form.api_placeholder)}
                  aria-invalid={!!apiError}
                  autoCapitalize="none"
                  autoCorrect="off"
                />
                {apiError ? (
                  <p className="text-caption text-destructive">{apiError}</p>
                ) : isPlainHttp(apiUrl) ? (
                  <p className="text-caption text-muted-foreground">
                    {t(($) => $.server.form.plain_http)}
                  </p>
                ) : null}
              </div>

              <div className="flex flex-col gap-2">
                <button
                  type="button"
                  onClick={() => setAdvancedOpen((v) => !v)}
                  className="flex items-center gap-2 py-1 text-caption text-muted-foreground hover:text-foreground"
                >
                  {advancedOpen ? (
                    <ChevronDown className="size-3.5" />
                  ) : (
                    <ChevronRight className="size-3.5" />
                  )}
                  {t(($) => $.server.form.advanced)}
                </button>
                {advancedOpen && (
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="server-web-url" className="text-caption text-muted-foreground">
                      {t(($) => $.server.form.web_hint)}
                    </Label>
                    <Input
                      id="server-web-url"
                      value={webUrl}
                      onChange={(e) => setWebUrl(e.target.value)}
                      placeholder={t(($) => $.server.form.web_placeholder)}
                      aria-invalid={!!webError}
                      autoCapitalize="none"
                      autoCorrect="off"
                    />
                    {webError ? (
                      <p className="text-caption text-destructive">{webError}</p>
                    ) : null}
                  </div>
                )}
              </div>

              <div className="flex flex-col gap-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => void onProbe()}
                  disabled={!apiValid || probe === "probing"}
                >
                  {probe === "probing" && <Loader2 className="size-3.5 animate-spin" />}
                  {probe === "probing"
                    ? t(($) => $.server.form.testing)
                    : t(($) => $.server.form.test)}
                </Button>
                {probe === "ok" ? (
                  <p className="text-caption text-emerald-600 dark:text-emerald-400">
                    {t(($) => $.server.form.connected)}
                  </p>
                ) : probe === "failed" ? (
                  <p className="text-caption text-destructive">
                    {t(($) => $.server.form.unreachable)}
                  </p>
                ) : null}
              </div>

              <DialogFooter>
                <Button variant="outline" onClick={onClose}>
                  {t(($) => $.server.form.cancel)}
                </Button>
                <Button onClick={onSaveClick} disabled={!canSave}>
                  {saving
                    ? t(($) => $.server.form.saving)
                    : t(($) => $.server.form.save)}
                </Button>
              </DialogFooter>
            </div>
          ) : (
            <div className="flex flex-col gap-2" data-testid="server-list">
              {servers.map((entry) => {
                const isActive = entry.id === activeServerId;
                const deletable = !entry.builtIn && !isActive;
                return (
                  <div
                    key={entry.id}
                    className="group/row flex items-center gap-2 rounded-md border bg-card px-3 py-2.5"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-body font-medium">
                          {entry.name || entry.apiUrl}
                        </span>
                        {entry.builtIn && (
                          <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-micro text-muted-foreground">
                            {t(($) => $.server.built_in)}
                          </span>
                        )}
                        {isActive && (
                          <span className="shrink-0 rounded bg-primary/10 px-1.5 py-0.5 text-micro font-medium text-primary">
                            {t(($) => $.server.current)}
                          </span>
                        )}
                      </div>
                      <p className="truncate text-caption text-muted-foreground">
                        {entry.apiUrl}
                      </p>
                    </div>
                    {!entry.builtIn && (
                      <div className="flex shrink-0 items-center gap-1">
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          aria-label={t(($) => $.server.edit)}
                          onClick={() => openForm(entry)}
                        >
                          <SquarePen className="size-3.5" />
                        </Button>
                        {deletable && (
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={t(($) => $.server.delete)}
                            onClick={() => setDeleteTarget(entry)}
                          >
                            <Trash2 className="size-3.5 text-destructive" />
                          </Button>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}

              {customServers.length === 0 && (
                <div
                  className="flex flex-col items-center gap-1 rounded-md border border-dashed px-4 py-6 text-center"
                  data-testid="server-list-empty"
                >
                  <p className="text-body font-medium">
                    {t(($) => $.server.empty_title)}
                  </p>
                  <p className="text-caption text-muted-foreground">
                    {t(($) => $.server.empty_description)}
                  </p>
                </div>
              )}

              <Button variant="outline" onClick={() => openForm("new")} className="mt-1">
                <Plus className="size-3.5" />
                {t(($) => $.server.add)}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={deleteTarget != null}
        onOpenChange={(next) => {
          if (!next) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.server.delete_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.server.delete_message, {
                name: deleteTarget?.name || deleteTarget?.apiUrl || "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.server.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={onDeleteConfirmed}
            >
              {t(($) => $.server.delete)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={confirmActiveUrlChange}
        onOpenChange={(next) => {
          if (!next) setConfirmActiveUrlChange(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.server.form.change_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.server.form.change_message)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.server.form.cancel)}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={onSaveConfirmed}>
              {t(($) => $.server.form.save)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
