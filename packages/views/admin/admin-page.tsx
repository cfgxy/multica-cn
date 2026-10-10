"use client";

import { ModelKeyPanel } from "./model-key-panel";
import { ModelTemplatePanel } from "./model-template-panel";
import { RuntimeCachePanel } from "./runtime-cache-panel";
import { LinuxUserDetail } from "../computers/linux-user-detail";

import { useEffect, useState } from "react";
import { useAuthStore } from "@multica/core/auth";
import {
  useInstanceAccess,
  useComputerAdmin,
  type AdminComputer,
  type ProbeResult,
} from "@multica/core/computers";
import { api } from "@multica/core/api";
import { paths } from "@multica/core/paths";
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
import { DropdownMenuItem } from "@multica/ui/components/ui/dropdown-menu";
import { Input } from "@multica/ui/components/ui/input";
import { AppLink, useNavigation } from "../navigation";
import { DragStrip } from "../platform";
import { useT } from "../i18n";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";

export function AdminAreaLink() {
  const user = useAuthStore((s) => s.user);
  const access = useInstanceAccess(user?.id ?? "");
  const { t } = useT("settings");
  return user && !access.error && access.data?.admin === true ? (
    <DropdownMenuItem render={<AppLink href={paths.admin()} />}>
      {t(($) => $.admin.title)}
    </DropdownMenuItem>
  ) : null;
}

/** The Admin Area sections, in sidebar order. */
const SECTIONS = ["computers", "bindings", "runtime-cache", "model-keys", "model-templates", "audit"] as const;
type Section = (typeof SECTIONS)[number];

export function AdminPage({ onBack }: { onBack?: () => void }) {
  const user = useAuthStore((s) => s.user);
  const loading = useAuthStore((s) => s.isLoading);
  const access = useInstanceAccess(user?.id ?? "");
  const { replace } = useNavigation();
  const { t } = useT("settings");
  const [section, setSection] = useState<Section>("computers");
  useEffect(() => {
    if (!loading && !user)
      replace(`${paths.login()}?next=${encodeURIComponent(paths.admin())}`);
  }, [loading, user, replace]);
  const admin = access.data?.admin === true;
  const label = (s: Section) =>
    s === "computers"
      ? t(($) => $.computers.title)
      : s === "bindings"
        ? t(($) => $.admin.bindings)
        : s === "model-keys" ? t(($) => $.model_configuration.keys_title) : s === "model-templates" ? t(($) => $.model_configuration.templates_title) : s === "runtime-cache" ? t(($) => $.runtime_cache.title) : t(($) => $.admin.audit);
  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <DragStrip />
      <header className="flex items-center justify-between border-b px-6 py-4">
        <div>
          <h1 className="text-xl font-semibold">{t(($) => $.admin.title)}</h1>
          <p className="text-sm text-muted-foreground">
            {t(($) => $.admin.scope)}
          </p>
        </div>
        {onBack ? (
          <Button variant="outline" onClick={onBack}>
            {t(($) => $.admin.back)}
          </Button>
        ) : (
          <AppLink href={paths.root()}>{t(($) => $.admin.back)}</AppLink>
        )}
      </header>
      <div className="flex flex-1 flex-col sm:flex-row">
        {admin && (
          <nav
            aria-label={t(($) => $.admin.sections)}
            className="shrink-0 border-b p-3 sm:w-56 sm:border-b-0 sm:border-r"
          >
            <ul className="flex flex-wrap gap-2 sm:flex-col">
              {SECTIONS.map((s) => (
                <li key={s}>
                  <Button
                    variant={section === s ? "secondary" : "ghost"}
                    aria-current={section === s ? "page" : undefined}
                    className="w-full justify-start"
                    onClick={() => setSection(s)}
                  >
                    {label(s)}
                  </Button>
                </li>
              ))}
            </ul>
          </nav>
        )}
        <main className="w-full max-w-6xl space-y-6 p-6">
          {loading || !user || access.isPending ? (
            <p role="status">{t(($) => $.computers.loading)}</p>
          ) : access.error ? (
            <p role="alert">{access.error.message}</p>
          ) : !admin ? (
            <p role="alert">{t(($) => $.admin.denied)}</p>
          ) : (
            section === "model-keys" ? <ModelKeyPanel key={user.id} userId={user.id} /> : section === "model-templates" ? <ModelTemplatePanel key={user.id} userId={user.id} /> : section === "runtime-cache" ? <RuntimeCachePanel userId={user.id} /> : <AdminSections key={user.id} userId={user.id} section={section} />
          )}
        </main>
      </div>
    </div>
  );
}

const EMPTY_FORM = { name: "", host: "", port: 22, ssh_user: "" };

function AdminSections({
  userId,
  section,
}: {
  userId: string;
  section: Section;
}) {
  const { t } = useT("settings");
  const data = useComputerAdmin(userId, true);
  const [detailBinding, setDetailBinding] = useState("");
  const [form, setForm] = useState(EMPTY_FORM);
  const [dialog, setDialog] = useState<"closed" | "add" | string>("closed");
  const [confirmDelete, setConfirmDelete] = useState<AdminComputer | null>(null);
  const [checkTarget, setCheckTarget] = useState<AdminComputer | null>(null);
  const [rowProbe, setRowProbe] = useState<ProbeResult | null>(null);
  const [rowError, setRowError] = useState("");
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [bindingSearch, setBindingSearch] = useState("");
  const [bindingChecks, setBindingChecks] = useState<Record<string, { present?: boolean; error?: string }>>({});
  const [checkingBinding, setCheckingBinding] = useState("");
  // data.check is the per-row dialog check; it must not freeze the rest of
  // the list while that dialog is open.
  const busy =
    data.register.isPending ||
    data.update.isPending ||
    data.remove.isPending ||
    data.checkDraft.isPending;
  const dialogClosed = dialog === "closed";
  useEffect(() => {
    setCheckTarget(null);
    setRowProbe(null);
    setRowError("");
  }, [section]);
  const closeDialog = () => {
    setDialog("closed");
    setForm(EMPTY_FORM);
    setProbe(null);
    setError("");
  };
  const perform = async (fn: () => Promise<unknown>, done?: () => void) => {
    setError("");
    setNotice("");
    try {
      await fn();
      done?.();
      setNotice(t(($) => $.admin.saved));
      return true;
    } catch (e) {
      // A failed connection check carries the per-requirement reasons.
      const body = (e as { body?: { probe?: unknown } } | null)?.body;
      if (body?.probe) setProbe(body.probe as ProbeResult);
      setError(e instanceof Error ? e.message : t(($) => $.computers.failed));
      return false;
    }
  };
  const openEdit = (m: AdminComputer) => {
    setProbe(null);
    setForm({ name: m.name, host: m.host, port: m.port, ssh_user: m.ssh_user });
    setDialog(m.id);
  };
  const name = (id: string) =>
    data.computers.data?.find((m) => m.id === id)?.name ?? id;
  const fetchError =
    data.computers.error || data.bindings.error || data.audit.error;
  const visibleBindings = data.bindings.data?.filter((b) =>
    [b.username, b.user_name, b.user_id, b.workspace_id, name(b.computer_id), b.state]
      .some((value) => value.toLowerCase().includes(bindingSearch.trim().toLowerCase())),
  );
  const checkBinding = async (id: string) => {
    setCheckingBinding(id);
    setBindingChecks((current) => ({ ...current, [id]: {} }));
    try {
      const result = await data.checkLinuxUser.mutateAsync(id);
      setBindingChecks((current) => ({ ...current, [id]: result }));
    } catch (e) {
      setBindingChecks((current) => ({ ...current, [id]: { error: e instanceof Error ? e.message : t(($) => $.computers.failed) } }));
    } finally {
      setCheckingBinding("");
    }
  };
  return (
    <>
      {/* While the form dialog is open it owns the feedback: a modal hides the
          page behind it, so an error rendered here would be unreachable.
          Probe and action notices stay inside Computers so a sidebar change
          drops them. */}
      {fetchError && dialogClosed && (
        <p role="alert" className="text-destructive">
          {fetchError.message}
        </p>
      )}
      {section === "computers" && (
        <section className="space-y-4" aria-labelledby="computer-registry">
          {error && dialogClosed && (
            <p role="alert" className="text-destructive">
              {error}
            </p>
          )}
          {notice && dialogClosed && <p role="status">{notice}</p>}
          <div className="flex items-center justify-between gap-3">
            <h2 id="computer-registry" className="text-lg font-semibold">
              {t(($) => $.computers.title)}
            </h2>
            <div className="flex gap-2">
              <CopySshPubKeyButton />
              <Button variant="outline" onClick={() => void data.refresh()}>
                {t(($) => $.admin.refresh)}
              </Button>
              <Button
                onClick={() => {
                  setProbe(null);
                  setForm(EMPTY_FORM);
                  setDialog("add");
                }}
              >
                {t(($) => $.admin.add)}
              </Button>
            </div>
          </div>
          <p className="text-sm text-muted-foreground">
            {t(($) => $.admin.disable_help)}
          </p>
          {data.computers.isPending && (
            <p role="status">{t(($) => $.computers.loading)}</p>
          )}
          {data.computers.data?.length === 0 && (
            <p>{t(($) => $.admin.no_computers)}</p>
          )}
          <ul className="space-y-3">
            {data.computers.data?.map((m) => (
              <ComputerRow
                key={m.id}
                machine={m}
                busy={busy}
                onEdit={() => openEdit(m)}
                onToggle={() =>
                  void perform(() =>
                    data.update.mutateAsync({ id: m.id, enabled: !m.enabled }),
                  )
                }
                onCheck={() => {
                  setCheckTarget(m);
                  setRowProbe(null);
                  setRowError("");
                  void data.check.mutateAsync(m.id).then(setRowProbe, (e) => {
                    const body = (e as { body?: { probe?: ProbeResult } } | null)
                      ?.body;
                    if (body?.probe) setRowProbe(body.probe);
                    setRowError(
                      e instanceof Error
                        ? e.message
                        : t(($) => $.computers.failed),
                    );
                  });
                }}
                onDelete={() => setConfirmDelete(m)}
              />
            ))}
          </ul>
        </section>
      )}
      {section === "bindings" && (
        <section className="space-y-4">
          <h2 className="text-lg font-semibold">{t(($) => $.admin.bindings)}</h2>
          <p className="text-sm text-muted-foreground">
            {t(($) => $.admin.bindings_help)}
          </p>
          <Input
            type="search"
            value={bindingSearch}
            onChange={(event) => setBindingSearch(event.target.value)}
            placeholder={t(($) => $.admin.linux_user_search)}
            aria-label={t(($) => $.admin.linux_user_search)}
          />
          <ul className="space-y-2">
            {visibleBindings?.map((b) => (
              <li className="rounded border p-3 space-y-2" key={b.id}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="min-w-0 break-all font-medium">{b.username}</p>
                  <Badge variant={b.state === "ready" ? "secondary" : b.state === "failed" ? "destructive" : "outline"}>
                    {b.state === "ready" ? t(($) => $.computers.ready) : b.state === "running" ? t(($) => $.computers.running) : b.state === "removed" ? t(($) => $.computers.removed) : b.state === "failed" ? t(($) => $.computers.failed) : b.state}
                  </Badge>
                </div>
                <dl className="grid gap-1 text-sm sm:grid-cols-2">
                  <div className="flex gap-2"><dt className="text-muted-foreground">{t(($) => $.admin.linux_user_computer)}</dt><dd className="break-all">{name(b.computer_id)}</dd></div>
                  <div className="flex gap-2"><dt className="text-muted-foreground">{t(($) => $.admin.linux_user_owner)}</dt><dd className="break-all">{b.user_name}</dd></div>
                  <div className="flex gap-2"><dt className="text-muted-foreground">{t(($) => $.admin.linux_user_workspace)}</dt><dd className="break-all">{b.workspace_id}</dd></div>
                </dl>
                {b.last_error && <p className="break-words text-sm text-destructive">{b.last_error}</p>}
                <div className="flex flex-wrap items-center gap-3">
                  <Button type="button" variant="outline" size="sm" disabled={checkingBinding !== ""} onClick={() => void checkBinding(b.id)}>
                    {checkingBinding === b.id ? t(($) => $.computers.loading) : t(($) => $.admin.linux_user_check)}
                  </Button>
                  {bindingChecks[b.id]?.present === true && <p role="status" className="text-sm">{t(($) => $.admin.linux_user_present)}</p>}
                  {bindingChecks[b.id]?.present === false && <p role="status" className="text-sm">{t(($) => $.admin.linux_user_missing)}</p>}
                  {bindingChecks[b.id]?.error && <p role="alert" className="break-words text-sm text-destructive">{bindingChecks[b.id]?.error}</p>}
                </div>
                <Button variant="outline" size="sm" aria-expanded={detailBinding === b.id} onClick={() => setDetailBinding(detailBinding === b.id ? "" : b.id)}>{t(($) => $.linux_user.details)}</Button>
                {detailBinding === b.id && <LinuxUserDetail bindingId={b.id} userId={userId} admin />}
              </li>
            ))}
          </ul>
          {visibleBindings?.length === 0 && <p>{bindingSearch ? t(($) => $.admin.linux_user_no_matches) : t(($) => $.admin.empty)}</p>}
        </section>
      )}
      {section === "audit" && (
        <section className="space-y-3">
          <h2 className="text-lg font-semibold">{t(($) => $.admin.audit)}</h2>
          <p className="text-sm text-muted-foreground">
            {t(($) => $.admin.audit_help)}
          </p>
          <ul className="space-y-2">
            {data.audit.data?.map((a) => (
              <li className="rounded border p-3 text-sm" key={a.id}>
                <time dateTime={a.created_at}>
                  {new Date(a.created_at).toLocaleString()}
                </time>{" "}
                · {a.user_name} · {name(a.computer_id)} · {a.action} ·{" "}
                {a.outcome}
              </li>
            ))}
          </ul>
          {data.audit.data?.length === 0 && <p>{t(($) => $.admin.empty)}</p>}
        </section>
      )}
      <ComputerFormDialog
        mode={dialog}
        form={form}
        busy={busy}
        onChange={setForm}
        onClose={closeDialog}
        error={error}
        probe={probe}
        onTest={() => {
          setProbe(null);
          setError("");
          void data.checkDraft.mutateAsync(form).then(
            (res) => setProbe(res),
            (e) => {
              const body = (e as { body?: { probe?: unknown } } | null)?.body;
              if (body?.probe) setProbe(body.probe as ProbeResult);
              setError(e instanceof Error ? e.message : t(($) => $.computers.failed));
            },
          );
        }}
        onSubmit={() =>
          void perform(
            () =>
              dialog === "add"
                ? data.register.mutateAsync(form)
                : data.update.mutateAsync({ id: dialog, ...form }),
            closeDialog,
          )
        }
      />
      <Dialog
        open={checkTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setCheckTarget(null);
            setRowProbe(null);
            setRowError("");
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {checkTarget?.name} · {t(($) => $.admin.test_connection)}
            </DialogTitle>
          </DialogHeader>
          {data.check.isPending && (
            <p role="status">{t(($) => $.computers.loading)}</p>
          )}
          {rowError && (
            <p role="alert" className="text-destructive">
              {rowError}
            </p>
          )}
          {rowProbe && <ProbeReport probe={rowProbe} />}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setCheckTarget(null);
                setRowProbe(null);
                setRowError("");
              }}
            >
              {t(($) => $.admin.cancel)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <DeleteComputerDialog
        machine={confirmDelete}
        busy={busy}
        onClose={() => setConfirmDelete(null)}
        onConfirm={() => {
          const target = confirmDelete;
          if (target)
            void perform(() => data.remove.mutateAsync(target.id), () =>
              setConfirmDelete(null),
            );
        }}
      />
    </>
  );
}

function CopySshPubKeyButton() {
  const { t } = useT("settings");
  const [busy, setBusy] = useState(false);
  const handleCopy = async () => {
    setBusy(true);
    try {
      const key = await api.getAdminSshPubKey();
      if (await copyText(key)) {
        toast.success(t(($) => $.admin.copy_ssh_pubkey_copied));
      } else {
        toast.error(t(($) => $.admin.copy_ssh_pubkey_failed));
      }
    } catch {
      toast.error(t(($) => $.admin.copy_ssh_pubkey_failed));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button variant="outline" disabled={busy} onClick={() => void handleCopy()}>
      {t(($) => $.admin.copy_ssh_pubkey)}
    </Button>
  );
}

/** One registry entry: identity, connection, and how it last checked out. */
function ComputerRow({
  machine: m,
  busy,
  onEdit,
  onToggle,
  onCheck,
  onDelete,
}: {
  machine: AdminComputer;
  busy: boolean;
  onEdit: () => void;
  onToggle: () => void;
  onCheck: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("settings");
  const when = (v?: string) => (v ? new Date(v).toLocaleString() : "");
  return (
    <li className="flex flex-col gap-3 rounded-lg border p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <p className="flex flex-wrap items-center gap-2 font-medium">
            {m.name}
            <Badge variant={m.enabled ? "secondary" : "outline"}>
              {m.enabled ? t(($) => $.admin.enabled) : t(($) => $.admin.disabled)}
            </Badge>
            {m.check_ok === true && (
              <Badge variant="secondary">{t(($) => $.admin.check_passed)}</Badge>
            )}
            {m.check_ok === false && (
              <Badge variant="destructive">{t(($) => $.admin.check_failed)}</Badge>
            )}
          </p>
          <p className="break-all text-sm text-muted-foreground">
            {m.ssh_user}@{m.host}:{m.port}
          </p>
          <dl className="grid gap-x-6 gap-y-1 text-sm text-muted-foreground sm:grid-cols-2">
            <div className="flex gap-1">
              <dt>{t(($) => $.admin.bound_accounts)}:</dt>
              <dd>{m.bindings}</dd>
            </div>
            {m.created_by_name && (
              <div className="flex gap-1">
                <dt>{t(($) => $.admin.registered_by)}:</dt>
                <dd className="truncate">{m.created_by_name}</dd>
              </div>
            )}
            {m.created_at && (
              <div className="flex gap-1">
                <dt>{t(($) => $.admin.registered_at)}:</dt>
                <dd>
                  <time dateTime={m.created_at}>{when(m.created_at)}</time>
                </dd>
              </div>
            )}
            <div className="flex gap-1">
              <dt>{t(($) => $.admin.last_check)}:</dt>
              <dd>
                {m.checked_at ? (
                  <time dateTime={m.checked_at}>{when(m.checked_at)}</time>
                ) : (
                  t(($) => $.admin.never_checked)
                )}
              </dd>
            </div>
          </dl>
          {m.check_ok === false && m.check_detail && (
            <p className="break-all text-sm text-destructive">{m.check_detail}</p>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" disabled={busy} onClick={onCheck}>
            {t(($) => $.admin.test_connection)}
          </Button>
          <Button variant="outline" disabled={busy} onClick={onEdit}>
            {t(($) => $.admin.edit)}
          </Button>
          <Button variant="outline" disabled={busy} onClick={onToggle}>
            {m.enabled ? t(($) => $.admin.disable) : t(($) => $.admin.enable)}
          </Button>
          <Button
            variant="outline"
            className="text-destructive"
            disabled={busy || m.bindings > 0}
            onClick={onDelete}
          >
            {t(($) => $.admin.delete)}
          </Button>
        </div>
      </div>
    </li>
  );
}

/** Per-requirement verdict from a connectivity check. */
function ProbeReport({ probe }: { probe: ProbeResult }) {
  const { t } = useT("settings");
  return (
    <section
      className="space-y-2 rounded-lg border p-4"
      aria-label={t(($) => $.admin.check_result)}
    >
      <p className="font-medium">
        {probe.ok
          ? t(($) => $.admin.check_passed)
          : t(($) => $.admin.check_failed)}
      </p>
      <ul className="space-y-1 text-sm">
        {probe.checks.map((c) => (
          <li
            key={c.name}
            className={c.ok ? "text-muted-foreground" : "text-destructive"}
          >
            {c.ok ? "✓" : "✗"} {c.name}
            {c.detail ? ` — ${c.detail}` : ""}
          </li>
        ))}
      </ul>
      {probe.facts.hostname && (
        <p className="text-sm text-muted-foreground">
          {[
            probe.facts.hostname,
            probe.facts.os,
            probe.facts.kernel,
            probe.facts.cpus ? `${probe.facts.cpus} CPU` : "",
            probe.facts.memory_mb
              ? `${Math.round(probe.facts.memory_mb / 1024)} GB`
              : "",
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
      )}
    </section>
  );
}

type FormState = typeof EMPTY_FORM;

function ComputerFormDialog({
  mode,
  form,
  busy,
  error,
  probe,
  onChange,
  onClose,
  onTest,
  onSubmit,
}: {
  mode: "closed" | "add" | string;
  form: FormState;
  busy: boolean;
  error: string;
  probe: ProbeResult | null;
  onChange: (f: FormState) => void;
  onClose: () => void;
  onTest: () => void;
  onSubmit: () => void;
}) {
  const { t } = useT("settings");
  const adding = mode === "add";
  return (
    <Dialog open={mode !== "closed"} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            onSubmit();
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {adding
                ? t(($) => $.computers.register)
                : t(($) => $.admin.edit)}
            </DialogTitle>
            <DialogDescription>
              {t(($) => $.admin.connection_help)}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            {(["name", "host", "ssh_user"] as const).map((k) => (
              <label key={k} className="space-y-1">
                <span>{t(($) => $.computers.machine_fields[k])}</span>
                <Input
                  required
                  value={form[k]}
                  onChange={(e) => onChange({ ...form, [k]: e.target.value })}
                />
              </label>
            ))}
            <label className="space-y-1">
              <span>{t(($) => $.computers.port)}</span>
              <Input
                required
                type="number"
                min={1}
                max={65535}
                value={form.port}
                onChange={(e) =>
                  onChange({ ...form, port: Number(e.target.value) })
                }
              />
            </label>
          </div>
          <p className="text-sm text-muted-foreground">
            {t(($) => $.admin.check_help)}
          </p>
          {error && (
            <p role="alert" className="text-destructive">
              {error}
            </p>
          )}
          {probe && <ProbeReport probe={probe} />}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={onTest}
            >
              {t(($) => $.admin.test_connection)}
            </Button>
            <Button type="button" variant="outline" onClick={onClose}>
              {t(($) => $.admin.cancel)}
            </Button>
            <Button type="submit" disabled={busy}>
              {adding
                ? t(($) => $.computers.register)
                : t(($) => $.admin.save)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function DeleteComputerDialog({
  machine,
  busy,
  onClose,
  onConfirm,
}: {
  machine: AdminComputer | null;
  busy: boolean;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const { t } = useT("settings");
  return (
    <Dialog open={!!machine} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.admin.delete_title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.admin.delete_confirm)}
          </DialogDescription>
        </DialogHeader>
        {machine && (
          <p className="break-all font-medium">
            {machine.name} · {machine.ssh_user}@{machine.host}:{machine.port}
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t(($) => $.admin.cancel)}
          </Button>
          <Button variant="destructive" disabled={busy} onClick={onConfirm}>
            {t(($) => $.admin.delete)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
