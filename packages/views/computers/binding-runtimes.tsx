"use client";

import { RuntimeModelDialog } from "./runtime-model-dialog";
import { ModelStatus } from "./model-config-controls";
import { modelRuntimeKinds } from "@multica/core/computers";
import { Fragment, useState } from "react";
import {
  ArrowUpCircle,
  Check,
  ChevronDown,
  Download,
  Loader2,
  RefreshCw,
  Settings2,
} from "lucide-react";
import {
  useComputerBindingRuntimes,
  useComputerBindingRuntimeInstall,
  useComputerBindingRuntimeBatch,
  type RuntimeBatchStatus,
  type AdminComputerRuntime,
} from "@multica/core/computers";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import { toast } from "sonner";
import { ProviderLogo } from "../runtimes/components/provider-logo";
import { useT } from "../i18n";

export function BindingRuntimes({
  accountLabel,
  bindingId,
  userId,
  busy,
}: {
  accountLabel?: string;
  bindingId: string;
  userId: string;
  busy: boolean;
}) {
  const { t } = useT("settings");
  const { runtimes, isInstalling } = useComputerBindingRuntimes(
    userId,
    bindingId,
    true,
    busy,
  );
  const [selected, setSelected] = useState<string[]>([]);
  const batch = useComputerBindingRuntimeBatch(userId, bindingId);
  const installable =
    runtimes.data?.filter((runtime) => runtime.can_install) ?? [];
  const chosen = installable.filter((runtime) => selected.includes(runtime.id));
  const locked = busy || isInstalling || batch.isPending;
  const batchLabels = t(
    ($) => $.linux_user_pages.runtime_versions.batch_states,
    { returnObjects: true },
  );
  const completed = [...batch.results.values()].filter((status) =>
    ["succeeded", "failed", "skipped"].includes(status),
  ).length;
  const failed = [...batch.results.values()].filter(
    (status) => status === "failed",
  ).length;
  return (
    <section
      className="space-y-4"
      aria-label={t(($) => $.linux_user_pages.views.runtimes)}
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-body font-semibold">
          {t(($) => $.linux_user_pages.views.runtimes)}
        </h3>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            disabled={locked || chosen.length === 0}
            aria-busy={batch.isPending}
            onClick={() => batch.start(chosen)}
          >
            {batch.isPending ? (
              <Loader2
                aria-hidden="true"
                className="motion-safe:animate-spin"
              />
            ) : (
              <Download aria-hidden="true" />
            )}
            {t(($) => $.linux_user_pages.runtime_versions.batch_install, {
              count: batch.isPending ? batch.results.size : chosen.length,
            })}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={runtimes.isFetching || isInstalling}
            aria-busy={runtimes.isFetching}
            onClick={() => void runtimes.refetch()}
          >
            <RefreshCw
              aria-hidden="true"
              className={
                runtimes.isFetching ? "motion-safe:animate-spin" : undefined
              }
            />
            {t(($) => $.admin.refresh)}
          </Button>
        </div>
      </div>
      <p className="text-caption text-muted-foreground">
        {t(($) => $.linux_user_pages.runtime_versions.batch_help)}
      </p>
      {batch.results.size > 0 && (
        <p
          role="status"
          className={
            batch.stopped
              ? "text-caption text-destructive"
              : "text-caption text-muted-foreground"
          }
        >
          {batch.stopped
            ? t(($) => $.linux_user_pages.runtime_versions.batch_stopped)
            : t(($) => $.linux_user_pages.runtime_versions.batch_progress, {
                completed,
                total: batch.results.size,
                failed,
              })}
        </p>
      )}
      {runtimes.isPending && (
        <p role="status">{t(($) => $.computers.loading)}</p>
      )}
      {runtimes.error && (
        <p role="alert" className="text-destructive">
          {runtimes.error.message}
        </p>
      )}
      {runtimes.isSuccess && runtimes.data.length === 0 && (
        <p>{t(($) => $.admin.runtimes_empty)}</p>
      )}
      {!!runtimes.data?.length && (
        <div className="overflow-hidden rounded-lg border bg-card">
          <Table
            aria-label={t(($) => $.linux_user_pages.views.runtimes)}
            className="min-w-[720px]"
          >
            <TableHeader className="bg-muted/40">
              <TableRow className="hover:bg-transparent">
                <TableHead className="w-10 pl-4">
                  <Checkbox
                    aria-label={t(
                      ($) => $.linux_user_pages.runtime_versions.select_all,
                    )}
                    checked={
                      installable.length > 0 &&
                      chosen.length === installable.length
                    }
                    indeterminate={
                      chosen.length > 0 && chosen.length < installable.length
                    }
                    disabled={locked || installable.length === 0}
                    onCheckedChange={(checked) =>
                      setSelected(
                        checked ? installable.map((runtime) => runtime.id) : [],
                      )
                    }
                  />
                </TableHead>
                <TableHead>{t(($) => $.admin.runtimes_title)}</TableHead>
                <TableHead>
                  {t(($) => $.linux_user_pages.runtime_versions.installed)}
                </TableHead>
                <TableHead>
                  {t(($) => $.linux_user_pages.runtime_versions.latest)}
                </TableHead>
                <TableHead>{t(($) => $.linux_user.registration)}</TableHead>
                <TableHead>{t(($) => $.model_configuration.configure)}</TableHead>
                <TableHead className="pr-4 text-right">
                  {t(($) => $.linux_user_pages.runtime_versions.actions)}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {runtimes.data.map((runtime) => (
                <RuntimeRow
                  accountLabel={accountLabel}
                  key={runtime.id}
                  runtime={runtime}
                  bindingId={bindingId}
                  userId={userId}
                  busy={locked}
                  selected={selected.includes(runtime.id)}
                  onSelect={(checked) =>
                    setSelected((current) =>
                      checked
                        ? [...current, runtime.id]
                        : current.filter((id) => id !== runtime.id),
                    )
                  }
                  batchStatus={batch.results.get(runtime.id)}
                  batchLabel={
                    batch.results.has(runtime.id)
                      ? batchLabels[batch.results.get(runtime.id)!]
                      : undefined
                  }
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <p className="text-caption text-muted-foreground">
        {t(($) => $.linux_user.discover_help)}
      </p>
      <details className="rounded-lg border bg-muted/20 px-4 py-3 text-caption text-muted-foreground">
        <summary className="w-fit cursor-pointer rounded-sm font-medium text-foreground focus-visible:outline-2 focus-visible:outline-ring">
          {t(($) => $.linux_user_pages.runtime_versions.install_help)}
        </summary>
        <p className="mt-2 max-w-2xl">
          {t(($) => $.admin.runtimes_prerequisites)}
        </p>
      </details>
    </section>
  );
}

function RuntimeRow({
  accountLabel,
  runtime,
  bindingId,
  userId,
  busy,
  selected,
  onSelect,
  batchStatus,
  batchLabel,
}: {
  runtime: AdminComputerRuntime;
  accountLabel?: string;
  bindingId: string;
  userId: string;
  busy: boolean;
  selected: boolean;
  onSelect: (checked: boolean) => void;
  batchStatus?: RuntimeBatchStatus;
  batchLabel?: string;
}) {
  const { t } = useT("settings");
  const [version, setVersion] = useState("");
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const install = useComputerBindingRuntimeInstall(
    userId,
    bindingId,
    runtime.id,
  );
  const errors = t(($) => $.linux_user.errors, { returnObjects: true });
  const supportsVersion = runtime.supports_version ?? runtime.version_required;
  const installed =
    !!runtime.installed_version && runtime.probe_state !== "missing";
  const latestReady =
    runtime.latest_version_state === "ready" && !!runtime.latest_version;
  const updateAvailable =
    installed &&
    latestReady &&
    runtime.update_available === true &&
    !runtime.probe_error;
  const upToDate =
    installed &&
    latestReady &&
    runtime.update_available === false &&
    !runtime.probe_error;
  const actionLabel = updateAvailable
    ? t(($) => $.admin.runtimes_update)
    : installed
      ? t(($) => $.linux_user_pages.runtime_versions.manage)
      : t(($) => $.admin.runtimes_install);
  const handleInstall = async () => {
    if (install.isPending || busy) return;
    try {
      await install.mutateAsync(
        supportsVersion
          ? version.trim() || (latestReady ? runtime.latest_version : "latest")
          : "latest",
      );
      toast.success(t(($) => $.computers.accepted));
      setOpen(false);
      setVersion("");
    } catch {
      toast.error(t(($) => $.admin.runtimes_install_failed));
    }
  };
  const detailsId = `runtime-assets-${runtime.id}`;
  return (
    <Fragment>
      <TableRow data-state={selected ? "selected" : undefined}>
        <TableCell className="pl-4">
          <Checkbox
            aria-label={t(
              ($) => $.linux_user_pages.runtime_versions.select_named,
              { runtime: runtime.display_name },
            )}
            checked={selected}
            disabled={busy || !runtime.can_install}
            onCheckedChange={onSelect}
          />
        </TableCell>
        <TableCell className="py-3">
          <div className="flex items-center gap-2.5 font-medium">
            <span aria-hidden="true">
              <ProviderLogo provider={runtime.id} className="size-5 shrink-0" />
            </span>
            {runtime.display_name}
          </div>
          {batchLabel && (
            <span
              className={`mt-1 inline-flex items-center gap-1 text-caption ${batchStatus === "failed" || batchStatus === "stopped" ? "text-destructive" : batchStatus === "succeeded" ? "text-success" : "text-muted-foreground"}`}
            >
              {batchStatus === "installing" && (
                <Loader2
                  aria-hidden="true"
                  className="size-3 motion-safe:animate-spin"
                />
              )}
              {batchLabel}
            </span>
          )}
          {runtime.probe_error && (
            <p className="mt-1 max-w-56 whitespace-normal text-caption text-destructive">
              {t(($) => $.admin.runtimes_probe_failed)}
            </p>
          )}
        </TableCell>
        <TableCell className="max-w-48 whitespace-normal break-words font-mono text-caption tabular-nums">
          {installed
            ? runtime.installed_version
            : runtime.probe_state === "missing"
              ? t(($) => $.admin.runtimes_not_installed)
              : t(($) => $.linux_user.unknown)}
        </TableCell>
        <TableCell className="max-w-40 whitespace-normal text-caption">
          {latestReady ? (
            <span className="font-mono tabular-nums">
              {runtime.latest_version}
            </span>
          ) : (
            <span className="text-muted-foreground">
              {runtime.latest_version_state === "checking"
                ? t(($) => $.linux_user_pages.runtime_versions.checking)
                : runtime.latest_version_state === "unsupported"
                  ? t(($) => $.linux_user_pages.runtime_versions.unsupported)
                  : t(($) => $.linux_user_pages.runtime_versions.unavailable)}
            </span>
          )}
        </TableCell>
        <TableCell className="text-caption">
          <span className="inline-flex items-center gap-1.5">
            <span
              aria-hidden="true"
              className={`size-1.5 rounded-full ${runtime.registration_state === "online" ? "bg-success" : "bg-muted-foreground/40"}`}
            />
            {runtime.registration_state === "online"
              ? t(($) => $.linux_user.states.online)
              : runtime.registration_state === "offline"
                ? t(($) => $.linux_user.states.offline)
                : t(($) => $.linux_user.states.not_discovered)}
          </span>
        </TableCell>
        <TableCell className="text-caption">
          {runtime.model_key_name && <p className="max-w-44 truncate font-medium" title={runtime.model_key_name}>{runtime.model_key_name}</p>}
          {runtime.model_id && <p className="max-w-44 truncate font-mono" title={runtime.model_id}>{runtime.model_id}</p>}
          <ModelStatus status={runtime.model_config_status ?? "unconfigured"} />
          <p><ModelStatus status={runtime.model_test_status ?? "untested"} /></p>
          {(runtime.model_test_status === "failed" || runtime.model_config_status === "needs_configuration") && runtime.model_config_error && <p className="max-w-48 whitespace-normal break-words text-destructive">{errors[runtime.model_config_error as keyof typeof errors] ?? runtime.model_config_error}</p>}
        </TableCell>
        <TableCell className="pr-4">
          <div className="flex items-center justify-end gap-2">
            {installed && modelRuntimeKinds.some((kind) => kind === runtime.id) && <RuntimeModelDialog accountLabel={accountLabel} userId={userId} bindingId={bindingId} kind={runtime.id} label={runtime.display_name} disabled={busy} />}
            {upToDate && (
              <span className="inline-flex items-center gap-1 text-caption text-success">
                <Check className="size-3.5" aria-hidden="true" />
                {t(($) => $.linux_user_pages.runtime_versions.up_to_date)}
              </span>
            )}
            {runtime.can_install && (
              <Dialog
                open={open}
                onOpenChange={(value) => {
                  if (!install.isPending) {
                    setOpen(value);
                    setVersion("");
                    install.reset();
                  }
                }}
              >
                <DialogTrigger
                  render={
                    <Button
                      type="button"
                      variant={
                        updateAvailable
                          ? "brandSubtle"
                          : upToDate
                            ? "ghost"
                            : "outline"
                      }
                      size={upToDate ? "icon-sm" : "sm"}
                      disabled={busy || install.isPending}
                      aria-label={
                        upToDate
                          ? t(
                              ($) =>
                                $.linux_user_pages.runtime_versions
                                  .manage_named,
                              { runtime: runtime.display_name },
                            )
                          : undefined
                      }
                      title={upToDate ? actionLabel : undefined}
                    />
                  }
                >
                  {updateAvailable ? (
                    <ArrowUpCircle aria-hidden="true" />
                  ) : installed ? (
                    <Settings2 aria-hidden="true" />
                  ) : (
                    <Download aria-hidden="true" />
                  )}
                  {!upToDate && actionLabel}
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>{runtime.display_name}</DialogTitle>
                  </DialogHeader>
                  <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-body">
                    <dt className="text-muted-foreground">
                      {t(($) => $.linux_user_pages.runtime_versions.installed)}
                    </dt>
                    <dd className="break-words font-mono">
                      {runtime.installed_version || "—"}
                    </dd>
                    <dt className="text-muted-foreground">
                      {t(($) => $.linux_user_pages.runtime_versions.latest)}
                    </dt>
                    <dd className="font-mono">
                      {latestReady ? runtime.latest_version : "—"}
                    </dd>
                  </dl>
                  <p className="text-caption text-muted-foreground">
                    {t(($) => $.admin.runtimes_prerequisites)}
                  </p>
                  {supportsVersion && (
                    <label className="space-y-1">
                      <span>
                        {t(($) => $.admin.runtimes_version_label, {
                          runtime: runtime.display_name,
                        })}
                      </span>
                      <Input
                        name="version"
                        autoComplete="off"
                        spellCheck={false}
                        placeholder={
                          latestReady
                            ? runtime.latest_version
                            : t(($) => $.admin.runtimes_version_placeholder)
                        }
                        value={version}
                        onChange={(event) => setVersion(event.target.value)}
                        disabled={install.isPending}
                      />
                    </label>
                  )}
                  {install.error && (
                    <p role="alert" className="text-destructive">
                      {install.error.message}
                    </p>
                  )}
                  <DialogFooter>
                    <Button
                      disabled={busy || install.isPending}
                      aria-busy={install.isPending}
                      onClick={() => void handleInstall()}
                    >
                      {install.isPending && (
                        <Loader2
                          className="motion-safe:animate-spin"
                          aria-hidden="true"
                        />
                      )}
                      {install.isPending
                        ? t(($) => $.admin.runtimes_installing)
                        : updateAvailable
                          ? t(($) => $.admin.runtimes_update)
                          : t(($) => $.admin.runtimes_install)}
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
            )}
            <Button
              variant="ghost"
              size="icon-sm"
              aria-expanded={expanded}
              aria-controls={detailsId}
              aria-label={t(
                ($) => $.linux_user_pages.runtime_versions.details_named,
                { runtime: runtime.display_name },
              )}
              onClick={() => setExpanded(!expanded)}
            >
              <ChevronDown
                aria-hidden="true"
                className={expanded ? "rotate-180" : undefined}
              />
            </Button>
          </div>
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow id={detailsId} className="bg-muted/20 hover:bg-muted/20">
          <TableCell colSpan={7} className="whitespace-normal px-4 py-3">
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-caption [&_dt]:text-muted-foreground [&_dd]:break-all">
              <dt>{t(($) => $.linux_user.path)}</dt>
              <dd>{runtime.executable_path || "—"}</dd>
              <dt>{t(($) => $.linux_user.source)}</dt>
              <dd>{runtime.installer_source || "—"}</dd>
              <dt>{t(($) => $.linux_user.checked)}</dt>
              <dd>
                {runtime.checked_at
                  ? new Date(runtime.checked_at).toLocaleString()
                  : "—"}
              </dd>
              <dt>{t(($) => $.linux_user_pages.runtime_versions.checked)}</dt>
              <dd>
                {runtime.latest_version_checked_at
                  ? new Date(runtime.latest_version_checked_at).toLocaleString()
                  : "—"}
              </dd>
              <dt>{t(($) => $.linux_user.environment)}</dt>
              <dd>{runtime.probe_environment || "—"}</dd>
            </dl>
            {runtime.probe_error && (
              <p className="mt-2 whitespace-pre-wrap break-words text-destructive">
                {errors[runtime.error_code as keyof typeof errors] ??
                  runtime.probe_error}
              </p>
            )}
          </TableCell>
        </TableRow>
      )}
    </Fragment>
  );
}
