"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertCircle, Blocks, Check, Loader2 } from "lucide-react";
import { toast } from "sonner";
import {
  pluginDirectoryOptions,
  pluginInstallationsOptions,
  useInstallPlugin,
  usePreviewPlugin,
} from "@multica/core/plugins";
import type { PluginPackage, PluginPackageVersion, PluginPreview } from "@multica/core/types";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n";
import { PluginConsent } from "./plugin-consent";
import { SettingsCard, SettingsSection } from "./settings-layout";

/**
 * The instance plugin directory, as the marketplace shows it.
 *
 * It is discovery only. Installing from here goes through exactly the same
 * preview-then-consent pair the Plugins tab uses, because the trust model does
 * not change with the shelf the plugin was found on: the administrator reading
 * the scope list is still the entire decision, and the version they approve is
 * still the version that runs.
 *
 * A withdrawn version is not offered — the publisher took it off the directory,
 * which is precisely the "do not install this one" signal. It is not hidden
 * from a workspace already running it; that lives on the Plugins tab, where the
 * installation is.
 */
export function PluginDirectory({
  wsId,
  canManage,
  search,
}: {
  wsId: string;
  /** Owner or admin. Everyone else browses. */
  canManage: boolean;
  search: string;
}) {
  const { t } = useT("settings");
  const { data, isLoading, isError } = useQuery(pluginDirectoryOptions(wsId));
  // plugins_v1 comes from the server, not a client-side flag read: the same
  // response the Plugins tab uses to decide whether it is in cleanup mode.
  // While it is in flight the directory offers nothing, which is the safe way
  // round — the endpoint answers 503 anyway.
  const installationsQuery = useQuery(pluginInstallationsOptions(wsId));
  const canInstall = canManage && installationsQuery.data?.plugins_enabled === true;
  const previewMutation = usePreviewPlugin(wsId);
  const installMutation = useInstallPlugin(wsId);
  const [preview, setPreview] = useState<PluginPreview | null>(null);

  const packages = useMemo(() => {
    const all = data?.packages ?? [];
    const term = search.trim().toLowerCase();
    if (term === "") return all;
    return all.filter((entry) =>
      `${entry.name} ${entry.plugin_key}`.toLowerCase().includes(term),
    );
  }, [data, search]);

  const reportError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t(($) => $.plugins.action_failed));
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
    <SettingsSection
      title={t(($) => $.marketplace.plugins_title)}
      description={t(($) => $.marketplace.plugins_description)}
    >
      <SettingsCard>
        {isLoading ? (
          <div className="px-4 py-4">
            <Skeleton className="h-16 w-full" aria-label={t(($) => $.plugins.loading)} />
          </div>
        ) : isError ? (
          <div className="px-4 py-4">
            <Alert variant="destructive">
              <AlertCircle />
              <AlertTitle>{t(($) => $.plugins.load_failed)}</AlertTitle>
              <AlertDescription>{t(($) => $.plugins.load_failed_description)}</AlertDescription>
            </Alert>
          </div>
        ) : packages.length === 0 ? (
          <div className="px-4 py-8 text-center">
            <Blocks className="mx-auto h-5 w-5 text-muted-foreground" />
            <p className="mt-3 text-body font-medium">
              {t(($) => $.marketplace.plugins_empty_title)}
            </p>
            <p className="mx-auto mt-1 max-w-md text-caption leading-5 text-muted-foreground">
              {t(($) => $.marketplace.plugins_empty_description)}
            </p>
          </div>
        ) : (
          <ul className="divide-y divide-surface-border">
            {packages.map((entry) => (
              <DirectoryRow
                key={entry.id}
                entry={entry}
                canInstall={canInstall}
                busy={previewMutation.isPending}
                onReview={review}
              />
            ))}
          </ul>
        )}

        {preview ? (
          <div className="border-t border-surface-border px-4 py-4">
            <PluginConsent
              preview={preview}
              installing={installMutation.isPending}
              canInstall={canInstall}
              onCancel={() => setPreview(null)}
              onConfirm={confirmInstall}
            />
          </div>
        ) : null}
      </SettingsCard>
    </SettingsSection>
  );
}

function DirectoryRow({
  entry,
  canInstall,
  busy,
  onReview,
}: {
  entry: PluginPackage;
  canInstall: boolean;
  busy: boolean;
  onReview: (versionId: string) => void;
}) {
  const { t } = useT("settings");
  // Newest first, so the first version still on the directory is the one to
  // offer. A withdrawn version is skipped rather than shown greyed out: the
  // publisher's statement is "do not start on this one", and rendering it as a
  // disabled row invites somebody to ask why they cannot have it.
  const offered: PluginPackageVersion | undefined = entry.versions.find(
    (version) => version.withdrawn_at === undefined || version.withdrawn_at === "",
  );
  const installed = entry.versions.find((version) => version.installed === true);

  return (
    <li className="flex items-start gap-3 px-4 py-3">
      <Blocks className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate text-body font-medium">{entry.name}</span>
          <Badge variant="secondary">{t(($) => $.marketplace.plugin_kind)}</Badge>
          {installed ? (
            <Badge variant="outline">
              <Check className="h-3 w-3" />
              {t(($) => $.marketplace.installed_badge)}
            </Badge>
          ) : null}
        </div>
        <p className="mt-0.5 text-caption text-muted-foreground">{entry.plugin_key}</p>
        {offered ? (
          <>
            {offered.description ? (
              <p className="mt-1 line-clamp-2 text-caption">{offered.description}</p>
            ) : null}
            <p className="mt-0.5 font-mono text-caption text-muted-foreground">
              {offered.version} · {offered.digest.slice(0, 12)}
            </p>
            {/*
              What it would be granted and what it would ask for, before the
              reader spends a consent round trip to find out. The scope strings
              are shown raw here for the same reason the consent screen does:
              summarizing them away is what turns a grant into a formality.
            */}
            {(offered.scopes ?? []).length > 0 ? (
              <p className="mt-1 flex flex-wrap gap-1 text-caption text-muted-foreground">
                {(offered.scopes ?? []).map((scope) => (
                  <code key={scope} className="rounded bg-muted px-1.5 py-0.5 font-mono">{scope}</code>
                ))}
              </p>
            ) : null}
            {(offered.config_keys ?? []).length > 0 ? (
              <p className="mt-1 text-caption text-muted-foreground">
                {t(($) => $.marketplace.plugin_config_summary, {
                  count: (offered.config_keys ?? []).length,
                  keys: (offered.config_keys ?? []).join("、"),
                })}
              </p>
            ) : null}
          </>
        ) : (
          <p className="mt-0.5 text-caption text-muted-foreground">
            {t(($) => $.marketplace.all_versions_withdrawn)}
          </p>
        )}
      </div>
      {canInstall && offered && offered.id !== installed?.id ? (
        <Button
          size="sm"
          variant={installed ? "outline" : "default"}
          className="shrink-0"
          disabled={busy}
          onClick={() => onReview(offered.id)}
        >
          {busy ? <Loader2 className="animate-spin" /> : null}
          {installed
            ? t(($) => $.plugins.publish.review_upgrade)
            : t(($) => $.plugins.publish.review_install)}
        </Button>
      ) : null}
    </li>
  );
}
