"use client";

import { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertCircle, CalendarClock, Globe, Loader2, Lock, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import { useCurrentMember } from "@multica/core/permissions";
import {
  pluginInstallationsOptions,
  pluginPackagesOptions,
  useClearPluginSecret,
  useConfigurePlugin,
  useDeletePluginPackage,
  useInstallPlugin,
  usePreviewPlugin,
  usePublishPluginPackage,
  useSetPluginEnabled,
  useSetPluginPackageVisibility,
  useSetPluginVersionWithdrawn,
  useUninstallPlugin,
} from "@multica/core/plugins";
import { useCurrentWorkspace } from "@multica/core/paths";
import type {
  PluginInstallation,
  PluginPackage,
  PluginPreview,
} from "@multica/core/types";
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
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Switch } from "@multica/ui/components/ui/switch";
import { mcpHooks, PluginHookActivity, PluginMCPApproval, PluginScheduleActivity } from "../../plugins";
import { useT } from "../../i18n";
import { ConfigFieldRow, configPayload } from "./plugin-config-field";
import { PluginConsent, PluginScheduleList, PluginScopeList } from "./plugin-consent";
import { SettingsCard, SettingsSection, SettingsTab } from "./settings-layout";

/**
 * The configuration form is generated from the manifest, not supplied by the
 * plugin: rendering plugin-authored form markup in the host would put plugin
 * code on our origin.
 */
function ConfigForm({
  installation,
  canManage,
  wsId,
}: {
  installation: PluginInstallation;
  canManage: boolean;
  wsId: string;
}) {
  const { t } = useT("settings");
  const configureMutation = useConfigurePlugin(wsId);
  const [values, setValues] = useState<Record<string, unknown>>(() => ({ ...installation.config }));
  const [secrets, setSecrets] = useState<Record<string, string>>({});

  if (installation.config_schema.length === 0) return null;

  const setValue = (key: string, value: unknown) => setValues((current) => ({ ...current, [key]: value }));
  const configuredSecrets = new Set(installation.configured_secrets);

  const submit = async () => {
    try {
      await configureMutation.mutateAsync({
        installationId: installation.id,
        values: configPayload(values, secrets),
      });
      setSecrets({});
      toast.success(t(($) => $.plugins.config.saved));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t(($) => $.plugins.action_failed));
    }
  };

  return (
    <div className="space-y-4 border-t border-surface-border px-4 py-4">
      <div className="text-caption font-medium">{t(($) => $.plugins.config.title)}</div>
      {installation.config_schema.map((field) => (
        <ConfigFieldRow
          key={field.key}
          field={field}
          value={values[field.key]}
          secretValue={secrets[field.key] ?? ""}
          configured={configuredSecrets.has(field.key)}
          disabled={!canManage || configureMutation.isPending}
          onValueChange={(value) => setValue(field.key, value)}
          onSecretChange={(value) => setSecrets((current) => ({ ...current, [field.key]: value }))}
        />
      ))}
      <div className="flex justify-end">
        <Button size="sm" disabled={!canManage || configureMutation.isPending} onClick={submit}>
          {configureMutation.isPending ? <Loader2 className="animate-spin" /> : null}
          {t(($) => $.plugins.config.save)}
        </Button>
      </div>
    </div>
  );
}

/**
 * Publishing, and installing what was published.
 *
 * These are one screen because they are two halves of one story: an author
 * uploads an artifact, and an administrator installs one specific version of it.
 * There is no way to install code that was not published here first — which is
 * what makes the consent screen below a statement about the code and not just
 * about the manifest.
 */
function PublishAndInstall({ wsId, canManage }: { wsId: string; canManage: boolean }) {
  const { t } = useT("settings");
  const { data, isLoading } = useQuery(pluginPackagesOptions(wsId));
  const publishMutation = usePublishPluginPackage(wsId);
  const deleteMutation = useDeletePluginPackage(wsId);
  const previewMutation = usePreviewPlugin(wsId);
  const installMutation = useInstallPlugin(wsId);
  const fileRef = useRef<HTMLInputElement>(null);
  const [preview, setPreview] = useState<PluginPreview | null>(null);

  const packages = useMemo(() => data?.packages ?? [], [data]);

  const reportError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t(($) => $.plugins.action_failed));
  };

  const publish = async (file: File) => {
    try {
      const published = await publishMutation.mutateAsync(file);
      toast.success(t(($) => $.plugins.publish.published, {
        name: published.name,
        version: published.versions[0]?.version ?? "",
      }));
    } catch (error) {
      reportError(error);
    } finally {
      // Cleared unconditionally so picking the same file again re-fires change,
      // which is what a person does after fixing a rejected bundle.
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  const review = async (versionId: string) => {
    try {
      setPreview(await previewMutation.mutateAsync({ version_id: versionId }));
    } catch (error) {
      setPreview(null);
      reportError(error);
    }
  };

  const confirmInstall = async (config: Record<string, unknown>) => {
    if (!preview) return;
    try {
      await installMutation.mutateAsync({
        version_id: preview.version_id,
        granted_scopes: preview.scopes,
        config,
      });
      setPreview(null);
      toast.success(t(($) => $.plugins.consent.installed));
    } catch (error) {
      reportError(error);
    }
  };

  return (
    <SettingsSection title={t(($) => $.plugins.publish.title)} description={t(($) => $.plugins.publish.description)}>
      <SettingsCard>
        <div className="flex flex-col gap-2 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-caption text-muted-foreground">{t(($) => $.plugins.publish.hint)}</p>
          <input
            ref={fileRef}
            type="file"
            accept=".zip,application/zip"
            className="sr-only"
            aria-label={t(($) => $.plugins.publish.upload)}
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) void publish(file);
            }}
          />
          <Button
            disabled={!canManage || publishMutation.isPending}
            onClick={() => fileRef.current?.click()}
          >
            {publishMutation.isPending ? <Loader2 className="animate-spin" /> : <Upload />}
            {t(($) => $.plugins.publish.upload)}
          </Button>
        </div>

        {isLoading ? (
          <div className="border-t border-surface-border px-4 py-4">
            <Skeleton className="h-16 w-full" aria-label={t(($) => $.plugins.loading)} />
          </div>
        ) : packages.length === 0 ? (
          <p className="border-t border-surface-border px-4 py-6 text-caption text-muted-foreground">
            {t(($) => $.plugins.publish.empty)}
          </p>
        ) : (
          packages.map((pluginPackage) => (
            <PublishedPackage
              key={pluginPackage.id}
              wsId={wsId}
              pluginPackage={pluginPackage}
              canManage={canManage}
              busy={previewMutation.isPending || deleteMutation.isPending}
              onReview={review}
              onDelete={(packageId) => deleteMutation
                .mutateAsync(packageId)
                .then(() => toast.success(t(($) => $.plugins.publish.deleted)))
                .catch(reportError)}
            />
          ))
        )}

        {preview ? (
          <div className="border-t border-surface-border px-4 py-4">
            <PluginConsent
              preview={preview}
              installing={installMutation.isPending}
              canInstall={canManage}
              onCancel={() => setPreview(null)}
              onConfirm={confirmInstall}
            />
          </div>
        ) : null}
      </SettingsCard>
    </SettingsSection>
  );
}

function PublishedPackage({
  wsId,
  pluginPackage,
  canManage,
  busy,
  onReview,
  onDelete,
}: {
  wsId: string;
  pluginPackage: PluginPackage;
  canManage: boolean;
  busy: boolean;
  onReview: (versionId: string) => void;
  onDelete: (packageId: string) => void;
}) {
  const { t } = useT("settings");
  const visibilityMutation = useSetPluginPackageVisibility(wsId);
  const withdrawMutation = useSetPluginVersionWithdrawn(wsId);
  // Versions arrive newest first, and the installed one is marked rather than
  // inferred: after a publish those are different rows, and that difference is
  // the whole reason an upgrade is a decision somebody makes.
  const installed = pluginPackage.versions.find((version) => version.installed === true);
  const isPublic = pluginPackage.visibility === "public";
  const listingBusy = visibilityMutation.isPending || withdrawMutation.isPending;

  const reportError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t(($) => $.plugins.action_failed));
  };

  return (
    <div className="space-y-3 border-t border-surface-border px-4 py-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-body font-medium">{pluginPackage.name}</span>
            <Badge variant={isPublic ? "secondary" : "outline"}>
              {isPublic ? <Globe className="h-3 w-3" /> : <Lock className="h-3 w-3" />}
              {isPublic
                ? t(($) => $.plugins.publish.listed_badge)
                : t(($) => $.plugins.publish.private_badge)}
            </Badge>
          </div>
          <p className="text-caption text-muted-foreground">{pluginPackage.plugin_key}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={!canManage || busy || listingBusy}
            onClick={() => visibilityMutation
              .mutateAsync({ packageId: pluginPackage.id, isPublic: !isPublic })
              .then(() => toast.success(isPublic
                ? t(($) => $.plugins.publish.unlisted)
                : t(($) => $.plugins.publish.listed)))
              .catch(reportError)}
          >
            {visibilityMutation.isPending ? <Loader2 className="animate-spin" /> : null}
            {isPublic
              ? t(($) => $.plugins.publish.unlist)
              : t(($) => $.plugins.publish.list)}
          </Button>
          <Button
            size="icon"
            variant="ghost"
            aria-label={t(($) => $.plugins.publish.delete)}
            disabled={!canManage || busy}
            onClick={() => onDelete(pluginPackage.id)}
          >
            <Trash2 />
          </Button>
        </div>
      </div>

      {/*
        Listing the package is what makes the directory show it; withdrawing a
        version is how a bad release comes off without unlisting the plugin
        wholesale. Both stop discovery and new installs only — a workspace
        already running a version keeps running it, which is what lets a
        publisher take either action at all.
      */}
      <p className="text-caption text-muted-foreground">
        {isPublic
          ? t(($) => $.plugins.publish.listed_note)
          : t(($) => $.plugins.publish.private_note)}
      </p>

      <ul className="space-y-1.5">
        {pluginPackage.versions.map((version) => {
          const withdrawn = version.withdrawn_at !== undefined && version.withdrawn_at !== "";
          return (
            <li key={version.id} className="flex flex-wrap items-center gap-2 text-caption">
              <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{version.version}</code>
              {version.installed === true ? (
                <Badge variant="secondary">{t(($) => $.plugins.publish.installed_version)}</Badge>
              ) : null}
              {withdrawn ? (
                <Badge variant="outline">{t(($) => $.plugins.publish.withdrawn_badge)}</Badge>
              ) : null}
              <span className="text-muted-foreground">{version.published_at.slice(0, 10)}</span>
              <span className="font-mono text-muted-foreground" title={version.digest}>
                {version.digest.slice(0, 12)}
              </span>
              <div className="ml-auto flex items-center gap-1">
                {isPublic ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={!canManage || busy || listingBusy}
                    onClick={() => withdrawMutation
                      .mutateAsync({ versionId: version.id, withdrawn: !withdrawn })
                      .then(() => toast.success(withdrawn
                        ? t(($) => $.plugins.publish.restored)
                        : t(($) => $.plugins.publish.withdrawn)))
                      .catch(reportError)}
                  >
                    {withdrawn
                      ? t(($) => $.plugins.publish.restore)
                      : t(($) => $.plugins.publish.withdraw)}
                  </Button>
                ) : null}
                {version.installed === true ? null : (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={!canManage || busy}
                    onClick={() => onReview(version.id)}
                  >
                    {installed ? t(($) => $.plugins.publish.review_upgrade) : t(($) => $.plugins.publish.review_install)}
                  </Button>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function InstalledPlugin({
  installation,
  wsId,
  canManage,
  canRemove,
}: {
  installation: PluginInstallation;
  wsId: string;
  /** Configure, enable/disable, approve MCP tools — off once plugins_v1 is. */
  canManage: boolean;
  /**
   * Uninstall only. Separate from canManage because removal outlives the
   * feature flag: an operator who turned plugins off still has to be able to
   * take out what was installed while they were on.
   */
  canRemove: boolean;
}) {
  const { t } = useT("settings");
  const enabledMutation = useSetPluginEnabled(wsId);
  const uninstallMutation = useUninstallPlugin(wsId);
  const isMutating = enabledMutation.isPending || uninstallMutation.isPending;
  const [confirmingUninstall, setConfirmingUninstall] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);

  const reportError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t(($) => $.plugins.action_failed));
  };

  const uninstall = async () => {
    try {
      await uninstallMutation.mutateAsync(installation.id);
      setConfirmingUninstall(false);
      toast.success(t(($) => $.plugins.uninstalled));
    } catch (error) {
      reportError(error);
    }
  };

  // Only rendered when a hook contributes one: a plugin with no hooks has no
  // failing calls to report.
  const hasHooks = (installation.hooks ?? []).length > 0;

  // An mcp hook is inert until its tools are approved, so the panel appears for
  // every one of them rather than only when something is already pinned.
  const mcpContributions = mcpHooks(installation.hooks ?? []);
  const scheduledHooks = (installation.hooks ?? []).filter((hook) => hook.schedule !== undefined);

  const contributions = [
    ...installation.surfaces.map((surface) => `${surface.name} (${surface.type})`),
    ...installation.hooks.map((hook) => `${hook.name} (${hook.triggers.join(", ")})`),
    ...installation.resources.map((resource) => `${resource.key} (${resource.type})`),
  ];

  return (
    <SettingsCard>
      <div className="space-y-4 px-4 py-4">
        {hasHooks ? <PluginHookActivity wsId={wsId} installationId={installation.id} /> : null}
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-body font-semibold">{installation.name}</span>
              <Badge variant="secondary">{t(($) => $.plugins.version, { version: installation.version })}</Badge>
              {!installation.enabled ? (
                <Badge variant="outline">{t(($) => $.plugins.states.disabled)}</Badge>
              ) : null}
            </div>
            <p className="text-caption text-muted-foreground">{installation.plugin_key}</p>
            {installation.description ? (
              <p className="mt-2 max-w-2xl text-caption">{installation.description}</p>
            ) : null}
          </div>
          <div className="flex items-center gap-2">
            <Switch
              aria-label={t(($) => $.plugins.enabled_label)}
              disabled={!canManage || isMutating}
              checked={installation.enabled}
              onCheckedChange={(checked) => enabledMutation
                .mutateAsync({ installationId: installation.id, enabled: checked === true })
                .then(() => toast.success(checked
                  ? t(($) => $.plugins.enabled)
                  : t(($) => $.plugins.disabled)))
                .catch(reportError)}
            />
            <Button
              size="icon"
              variant="ghost"
              aria-label={t(($) => $.plugins.uninstall)}
              disabled={!canRemove || isMutating}
              onClick={() => {
                setAcknowledged(false);
                setConfirmingUninstall(true);
              }}
            >
              {uninstallMutation.isPending ? <Loader2 className="animate-spin" /> : <Trash2 />}
            </Button>
          </div>
        </div>

        {/*
          Uninstall deletes in one transaction, and none of it comes back. The
          list is spelled out rather than summarized as "all data" because the
          reader is being asked to accept a specific loss — contributed skills
          and stored credentials in particular are things a workspace can be
          depending on without the person clicking the icon knowing it.
        */}
        <AlertDialog open={confirmingUninstall} onOpenChange={setConfirmingUninstall}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t(($) => $.plugins.uninstall_dialog.title, { name: installation.name })}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.plugins.uninstall_dialog.description)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <Alert variant="destructive">
              <AlertCircle />
              <AlertTitle>{t(($) => $.plugins.uninstall_dialog.deletes_title)}</AlertTitle>
              <AlertDescription>
                <ul className="list-disc space-y-1 pl-4">
                  <li>{t(($) => $.plugins.uninstall_dialog.deletes_storage)}</li>
                  <li>{t(($) => $.plugins.uninstall_dialog.deletes_config)}</li>
                  <li>
                    {installation.configured_secrets.length > 0
                      ? t(($) => $.plugins.uninstall_dialog.deletes_secrets_named, {
                          keys: installation.configured_secrets.join("、"),
                        })
                      : t(($) => $.plugins.uninstall_dialog.deletes_secrets)}
                  </li>
                  <li>{t(($) => $.plugins.uninstall_dialog.deletes_skills)}</li>
                  <li>{t(($) => $.plugins.uninstall_dialog.deletes_hooks)}</li>
                </ul>
              </AlertDescription>
            </Alert>
            <label className="flex items-start gap-2 text-caption">
              <Checkbox
                className="mt-0.5"
                checked={acknowledged}
                onCheckedChange={(checked) => setAcknowledged(checked === true)}
              />
              <span>{t(($) => $.plugins.uninstall_dialog.acknowledge)}</span>
            </label>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={uninstallMutation.isPending}>
                {t(($) => $.plugins.uninstall_dialog.cancel)}
              </AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                disabled={!acknowledged || uninstallMutation.isPending}
                onClick={() => void uninstall()}
              >
                {uninstallMutation.isPending ? <Loader2 className="animate-spin" /> : <Trash2 />}
                {t(($) => $.plugins.uninstall_dialog.confirm)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>

        <div className="space-y-2">
          <div className="text-caption font-medium">{t(($) => $.plugins.granted_scopes)}</div>
          <PluginScopeList scopes={installation.granted_scopes} />
        </div>

        {scheduledHooks.length > 0 ? (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-caption font-medium">
              <CalendarClock className="size-4" />
              {t(($) => $.plugins.schedule.title)}
            </div>
            <PluginScheduleList hooks={scheduledHooks} showNextRun />
            <PluginScheduleActivity
              wsId={wsId}
              installationId={installation.id}
              hooks={scheduledHooks}
            />
          </div>
        ) : null}

        {mcpContributions.length > 0 ? (
          <div className="space-y-2">
            <div className="text-caption font-medium">{t(($) => $.plugins.mcp.badge)}</div>
            {mcpContributions.map((hook) => (
              <PluginMCPApproval
                key={hook.key}
                wsId={wsId}
                installationId={installation.id}
                hook={hook}
                canManage={canManage}
              />
            ))}
          </div>
        ) : null}

        {contributions.length > 0 ? (
          <div className="space-y-1">
            <div className="text-caption font-medium">{t(($) => $.plugins.contributes)}</div>
            <p className="text-caption text-muted-foreground">{contributions.join(" · ")}</p>
          </div>
        ) : null}

      </div>

      {/*
        Only one of the two: an admin whose flag is off has no configuration
        form to edit, and per-secret removal is the cleanup lever that replaces
        it. Everyone else keeps the form, read-only when they cannot manage.
      */}
      {canRemove && !canManage ? (
        <SecretCleanup installation={installation} wsId={wsId} />
      ) : (
        <ConfigForm installation={installation} canManage={canManage} wsId={wsId} />
      )}
    </SettingsCard>
  );
}

/**
 * Removing one stored credential without uninstalling the plugin.
 *
 * This is the cleanup surface that has to exist while plugins_v1 is off: the
 * configuration form is gone with the flag, so without it the only way to get a
 * leaked credential out of the database would be to remove the whole
 * installation. Names only — a stored secret's value is never returned by any
 * endpoint, including this one.
 */
function SecretCleanup({
  installation,
  wsId,
}: {
  installation: PluginInstallation;
  wsId: string;
}) {
  const { t } = useT("settings");
  const clearMutation = useClearPluginSecret(wsId);
  const [pendingKey, setPendingKey] = useState<string | null>(null);

  if (installation.configured_secrets.length === 0) return null;

  const clear = async (key: string) => {
    try {
      await clearMutation.mutateAsync({ installationId: installation.id, key });
      setPendingKey(null);
      toast.success(t(($) => $.plugins.secret_cleanup.cleared, { key }));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t(($) => $.plugins.action_failed));
    }
  };

  return (
    <div className="space-y-3 border-t border-surface-border px-4 py-4">
      <div>
        <div className="flex items-center gap-1.5 text-caption font-medium">
          <Lock className="size-3 shrink-0 text-muted-foreground" />
          {t(($) => $.plugins.secret_cleanup.title)}
        </div>
        <p className="mt-0.5 text-caption text-muted-foreground">
          {t(($) => $.plugins.secret_cleanup.description)}
        </p>
      </div>
      <ul className="space-y-1.5">
        {installation.configured_secrets.map((key) => (
          <li key={key} className="flex flex-wrap items-center gap-2 text-caption">
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{key}</code>
            <div className="ml-auto flex items-center gap-2">
              {pendingKey === key ? (
                <>
                  <span className="text-muted-foreground">
                    {t(($) => $.plugins.secret_cleanup.confirm_hint)}
                  </span>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={clearMutation.isPending}
                    onClick={() => setPendingKey(null)}
                  >
                    {t(($) => $.plugins.secret_cleanup.cancel)}
                  </Button>
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={clearMutation.isPending}
                    onClick={() => void clear(key)}
                  >
                    {clearMutation.isPending ? <Loader2 className="animate-spin" /> : null}
                    {t(($) => $.plugins.secret_cleanup.confirm)}
                  </Button>
                </>
              ) : (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={clearMutation.isPending}
                  onClick={() => setPendingKey(key)}
                >
                  {t(($) => $.plugins.secret_cleanup.clear)}
                </Button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function PluginsTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const { role } = useCurrentMember(wsId);
  const canManage = role === "owner" || role === "admin";

  const { data, isLoading, isError } = useQuery(pluginInstallationsOptions(wsId));
  const installations = useMemo(() => data?.plugins ?? [], [data]);
  // The server decides, not a client-side flag read: listing and uninstall stay
  // open when plugins_v1 is off precisely so an operator can clean up, and the
  // response says which mode it answered in. Defaulting to enabled while the
  // query is in flight keeps the first paint from flashing the disabled banner
  // at a workspace where nothing is wrong.
  const pluginsEnabled = data?.plugins_enabled ?? true;
  // Everything that would start new plugin work is gated on both: publishing
  // and installing are what the flag closes, and only an owner or admin could
  // do them anyway.
  const canInstall = canManage && pluginsEnabled;

  return (
    <SettingsTab title={t(($) => $.plugins.title)} description={t(($) => $.plugins.description)}>
      {!pluginsEnabled ? (
        <Alert>
          <AlertCircle />
          <AlertTitle>{t(($) => $.plugins.disabled_title)}</AlertTitle>
          <AlertDescription>{t(($) => $.plugins.disabled_description)}</AlertDescription>
        </Alert>
      ) : null}

      {!canManage ? (
        <Alert>
          <AlertCircle />
          <AlertTitle>{t(($) => $.plugins.read_only)}</AlertTitle>
          <AlertDescription>{t(($) => $.plugins.read_only_description)}</AlertDescription>
        </Alert>
      ) : null}

      {canInstall ? <PublishAndInstall wsId={wsId} canManage={canInstall} /> : null}

      <SettingsSection title={t(($) => $.plugins.installed.title)}>
        {isLoading ? (
          <Skeleton className="h-24 w-full" aria-label={t(($) => $.plugins.loading)} />
        ) : isError ? (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertTitle>{t(($) => $.plugins.load_failed)}</AlertTitle>
            <AlertDescription>{t(($) => $.plugins.load_failed_description)}</AlertDescription>
          </Alert>
        ) : installations.length === 0 ? (
          <SettingsCard>
            <p className="px-4 py-6 text-caption text-muted-foreground">{t(($) => $.plugins.empty)}</p>
          </SettingsCard>
        ) : (
          <div className="space-y-4">
            {installations.map((installation) => (
              <InstalledPlugin
                key={installation.id}
                installation={installation}
                wsId={wsId}
                canManage={canInstall}
                canRemove={canManage}
              />
            ))}
          </div>
        )}
      </SettingsSection>
    </SettingsTab>
  );
}
