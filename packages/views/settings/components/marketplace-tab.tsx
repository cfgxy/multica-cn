"use client";

import { useMemo, useState } from "react";
import { Check, Loader2, Search, Server, Sparkles, Store } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
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
import { Input } from "@multica/ui/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { ApiError } from "@multica/core/api";
import { useFeatureEnabled } from "@multica/core/config";
import { MARKETPLACE_PUBLISH_V1_FLAG } from "@multica/core/feature-flags";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useCurrentMember } from "@multica/core/permissions";
import { pluginInstallationsOptions } from "@multica/core/plugins";
import {
  marketplaceItemsOptions,
  marketplaceListingsOptions,
} from "@multica/core/workspace/queries";
import {
  useInstallMarketplaceItem,
  usePublishMarketplaceListing,
  useUpdateMarketplaceListing,
  useWithdrawMarketplaceListing,
} from "@multica/core/workspace/mutations";
import type {
  MarketplaceItem,
  MarketplaceListing,
  MarketplaceScanFinding,
} from "@multica/core/types";
import { useT } from "../../i18n";
import { MarketplaceInstallDialog } from "./marketplace-install-dialog";
import { PluginDirectory } from "./plugin-directory";
import {
  MarketplacePublishDialog,
  type MarketplacePublishSubmit,
} from "./marketplace-publish-dialog";
import { SettingsCard, SettingsSection, SettingsTab } from "./settings-layout";

/**
 * The unified application marketplace.
 *
 * It is a discovery and install surface over capabilities that already exist:
 * a skill install goes through the same import path a hand-typed URL takes,
 * and an MCP install becomes an ordinary workspace MCP library entry. Nothing
 * here is a second runtime, so an installed item behaves exactly like one
 * added by hand — including the fact that it reaches NO agent until someone
 * binds it on the agent's own tab.
 */
export function MarketplaceTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const currentMember = useCurrentMember(wsId);
  const canManage =
    currentMember.role === "owner" || currentMember.role === "admin";

  // The write path has its own flag: the catalog can stay open while
  // publishing is still off, and turning publishing off leaves already
  // published listings exactly where they are.
  const publishEnabled = useFeatureEnabled(MARKETPLACE_PUBLISH_V1_FLAG, false);

  const [kind, setKind] = useState("");
  const [search, setSearch] = useState("");
  // Two shelves, one filter. The catalog is a build-time list the server
  // filters; the plugin directory is what workspaces on this instance have
  // listed at runtime. They cannot be merged into one query, so the filter
  // picks which sections render and the catalog query stands down when the
  // reader asked for plugins only.
  //
  // The plugin shelf is behind plugins_v1, and the flag lives on the server.
  // The installed-plugins response is what reports it — that endpoint answers
  // with the flag off by design, which is exactly what makes it usable as the
  // signal here. Until it answers, the shelf stays out rather than rendering a
  // filter that would disappear a moment later.
  const installationsQuery = useQuery(pluginInstallationsOptions(wsId));
  const pluginsEnabled = installationsQuery.data?.plugins_enabled === true;
  // A reader already filtered to plugins when the flag went off falls back to
  // the catalog rather than to an empty page.
  const showCatalog = kind !== "plugin" || !pluginsEnabled;
  const showPlugins = pluginsEnabled && (kind === "" || kind === "plugin");
  const itemsQuery = useQuery({
    ...marketplaceItemsOptions(wsId, { kind, q: search }),
    enabled: showCatalog && wsId.length > 0,
  });
  const install = useInstallMarketplaceItem(wsId);

  const items = useMemo(() => itemsQuery.data ?? [], [itemsQuery.data]);
  // Server names must stay unique in the library; knowing what is taken lets
  // the dialog say so before the install can only come back 409.
  const takenNames = useMemo(
    () =>
      new Set(
        items
          .filter((item) => item.kind === "mcp" && item.installed)
          .map((item) => item.name),
      ),
    [items],
  );

  const [installTarget, setInstallTarget] = useState<MarketplaceItem | null>(null);

  // Only fetched when the workspace can actually manage listings — an ordinary
  // member has no management section to fill, so there is nothing to load.
  const canPublish = canManage && publishEnabled;
  const listingsQuery = useQuery({
    ...marketplaceListingsOptions(wsId),
    enabled: wsId !== "" && canPublish,
  });
  const listings = useMemo(() => listingsQuery.data ?? [], [listingsQuery.data]);

  const publish = usePublishMarketplaceListing(wsId);
  const update = useUpdateMarketplaceListing(wsId);
  const withdraw = useWithdrawMarketplaceListing(wsId);

  // `publishKind` doubles as the open flag: a dialog is open exactly when a
  // kind has been chosen, and editing carries the listing alongside it.
  const [publishKind, setPublishKind] = useState("");
  const [editing, setEditing] = useState<MarketplaceListing | null>(null);
  const [withdrawing, setWithdrawing] = useState<MarketplaceListing | null>(null);
  const [findings, setFindings] = useState<MarketplaceScanFinding[]>([]);
  const [scannerRevision, setScannerRevision] = useState("");
  const [publishError, setPublishError] = useState("");

  const closePublishDialog = () => {
    setPublishKind("");
    setEditing(null);
    setFindings([]);
    setScannerRevision("");
    setPublishError("");
  };

  const openPublishDialog = (nextKind: string, listing: MarketplaceListing | null) => {
    setFindings([]);
    setScannerRevision("");
    setPublishError("");
    setEditing(listing);
    setPublishKind(nextKind);
  };

  const handlePublish = async (input: MarketplacePublishSubmit) => {
    setFindings([]);
    setScannerRevision("");
    setPublishError("");
    try {
      // A withdrawn listing cannot be edited — the server refuses a PATCH on a
      // tombstone and says to publish again instead, which is exactly what the
      // republish button does: the same name goes back through publish, which
      // revives the row this workspace still owns.
      if (editing && editing.state !== "withdrawn") {
        // `kind` is fixed at publish time and the update route does not accept
        // it, so it is dropped rather than sent as a field the server ignores.
        const { kind: _kind, ...editable } = input;
        await update.mutateAsync({
          id: editing.id,
          revision: editing.revision,
          ...editable,
        });
        toast.success(t(($) => $.marketplace.updated_toast, { name: input.name }));
      } else {
        await publish.mutateAsync(input);
        toast.success(t(($) => $.marketplace.published_toast, { name: input.name }));
      }
      closePublishDialog();
    } catch (error) {
      // A 422 is the secret scan refusing the content. Its body carries the
      // findings, which are locations only — field, line, rule — so they can be
      // shown without echoing whatever was pasted.
      const scan = scanErrorOf(error);
      if (scan) {
        setFindings(scan.findings);
        setScannerRevision(scan.scannerRevision);
        return;
      }
      setPublishError(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.marketplace.publish_failed_toast),
      );
    }
  };

  const handleWithdraw = async () => {
    if (!withdrawing) return;
    try {
      await withdraw.mutateAsync({
        id: withdrawing.id,
        revision: withdrawing.revision,
      });
      toast.success(
        t(($) => $.marketplace.withdrawn_toast, { name: withdrawing.name }),
      );
      setWithdrawing(null);
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.marketplace.withdraw_failed_toast),
      );
    }
  };

  const handleInstall = async ({
    name,
    values,
  }: {
    name: string;
    values: Record<string, string>;
  }) => {
    if (!installTarget) return;
    try {
      await install.mutateAsync({
        key: installTarget.key,
        name: name || undefined,
        values,
      });
      toast.success(t(($) => $.marketplace.installed_toast, { name: installTarget.name }));
      setInstallTarget(null);
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.marketplace.install_failed_toast),
      );
    }
  };

  return (
    <SettingsTab
      title={t(($) => $.marketplace.title)}
      description={t(($) => $.marketplace.description)}
    >
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <Tabs value={kind} onValueChange={setKind}>
          <TabsList>
            <TabsTrigger value="">{t(($) => $.marketplace.filter_all)}</TabsTrigger>
            <TabsTrigger value="skill">{t(($) => $.marketplace.filter_skills)}</TabsTrigger>
            <TabsTrigger value="mcp">{t(($) => $.marketplace.filter_mcp)}</TabsTrigger>
            {pluginsEnabled ? (
              <TabsTrigger value="plugin">{t(($) => $.marketplace.filter_plugins)}</TabsTrigger>
            ) : null}
          </TabsList>
        </Tabs>
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-8"
            value={search}
            placeholder={t(($) => $.marketplace.search_placeholder)}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
      </div>

      {showCatalog ? (
      <SettingsSection
        title={t(($) => $.marketplace.browse_title)}
        description={t(($) => $.marketplace.binding_note)}
      >
        <SettingsCard>
          {itemsQuery.isLoading ? (
            <div className="flex items-center justify-center py-8 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
            </div>
          ) : items.length === 0 ? (
            <div className="px-4 py-8 text-center">
              <Store className="mx-auto h-5 w-5 text-muted-foreground" />
              <p className="mt-3 text-body font-medium">
                {t(($) => $.marketplace.empty_title)}
              </p>
              <p className="mx-auto mt-1 max-w-md text-caption leading-5 text-muted-foreground">
                {t(($) => $.marketplace.empty_description)}
              </p>
            </div>
          ) : (
            <ul className="divide-y divide-surface-border">
              {items.map((item) => (
                <MarketplaceItemRow
                  key={item.key}
                  item={item}
                  canManage={canManage}
                  onInstall={() => setInstallTarget(item)}
                />
              ))}
            </ul>
          )}
        </SettingsCard>
        {!canManage && !currentMember.isLoading ? (
          <p className="px-0.5 text-caption text-muted-foreground">
            {t(($) => $.marketplace.admin_only_note)}
          </p>
        ) : null}
      </SettingsSection>
      ) : null}

      {showPlugins ? (
        <PluginDirectory wsId={wsId} canManage={canManage} search={search} />
      ) : null}

      {canPublish ? (
        <SettingsSection
          title={t(($) => $.marketplace.published_title)}
          description={t(($) => $.marketplace.published_description)}
        >
          <div className="mb-3 flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => openPublishDialog("skill", null)}
            >
              <Sparkles className="h-4 w-4" />
              {t(($) => $.marketplace.publish_skill)}
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => openPublishDialog("mcp", null)}
            >
              <Server className="h-4 w-4" />
              {t(($) => $.marketplace.publish_mcp)}
            </Button>
          </div>

          <SettingsCard>
            {listingsQuery.isLoading ? (
              <div className="flex items-center justify-center py-8 text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
              </div>
            ) : listings.length === 0 ? (
              <p className="px-4 py-8 text-center text-caption text-muted-foreground">
                {t(($) => $.marketplace.published_empty)}
              </p>
            ) : (
              <ul className="divide-y divide-surface-border">
                {listings.map((listing) => (
                  <MarketplaceListingRow
                    key={listing.id}
                    listing={listing}
                    onEdit={() => openPublishDialog(listing.kind, listing)}
                    onWithdraw={() => setWithdrawing(listing)}
                    onRepublish={() => openPublishDialog(listing.kind, listing)}
                  />
                ))}
              </ul>
            )}
          </SettingsCard>
        </SettingsSection>
      ) : canManage && !currentMember.isLoading ? (
        <p className="px-0.5 text-caption text-muted-foreground">
          {t(($) => $.marketplace.publish_disabled_note)}
        </p>
      ) : null}

      <MarketplacePublishDialog
        open={publishKind !== ""}
        kind={publishKind}
        listing={editing}
        submitting={publish.isPending || update.isPending}
        findings={findings}
        scannerRevision={scannerRevision}
        errorMessage={publishError}
        onOpenChange={(open) => {
          if (!open) closePublishDialog();
        }}
        onSubmit={(input) => void handlePublish(input)}
      />

      <AlertDialog
        open={withdrawing !== null}
        onOpenChange={(open) => {
          if (!open) setWithdrawing(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.marketplace.withdraw_title, {
                name: withdrawing?.name ?? "",
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.marketplace.withdraw_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={withdraw.isPending}>
              {t(($) => $.marketplace.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault();
                void handleWithdraw();
              }}
              disabled={withdraw.isPending}
            >
              {withdraw.isPending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : null}
              {t(($) => $.marketplace.withdraw_confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <MarketplaceInstallDialog
        open={installTarget !== null}
        item={installTarget}
        installing={install.isPending}
        existingNames={takenNames}
        onOpenChange={(open) => {
          if (!open) setInstallTarget(null);
        }}
        onInstall={(input) => void handleInstall(input)}
      />
    </SettingsTab>
  );
}

function MarketplaceItemRow({
  item,
  canManage,
  onInstall,
}: {
  item: MarketplaceItem;
  canManage: boolean;
  onInstall: () => void;
}) {
  const { t } = useT("settings");
  const Icon = item.kind === "skill" ? Sparkles : Server;
  // A kind this client does not know how to install still lists — it just
  // cannot offer the button, which is the honest state.
  const installable = item.kind === "skill" || item.kind === "mcp";

  return (
    <li className="flex items-start gap-3 px-4 py-3">
      <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate text-body font-medium">{item.name}</span>
          <Badge variant="secondary">{kindLabel(item.kind)}</Badge>
          {item.installed ? (
            <Badge variant="outline">
              <Check className="h-3 w-3" />
              {t(($) => $.marketplace.installed_badge)}
            </Badge>
          ) : null}
        </div>
        <p className="mt-0.5 text-caption text-muted-foreground">{item.summary}</p>
        {item.publisher ? (
          <p className="mt-0.5 text-caption text-muted-foreground">{item.publisher}</p>
        ) : null}
      </div>
      {canManage && installable ? (
        <Button
          size="sm"
          variant={item.installed ? "outline" : "default"}
          className="shrink-0"
          onClick={onInstall}
        >
          {item.installed
            ? t(($) => $.marketplace.install_again)
            : t(($) => $.marketplace.install)}
        </Button>
      ) : null}
    </li>
  );
}

function MarketplaceListingRow({
  listing,
  onEdit,
  onWithdraw,
  onRepublish,
}: {
  listing: MarketplaceListing;
  onEdit: () => void;
  onWithdraw: () => void;
  onRepublish: () => void;
}) {
  const { t } = useT("settings");
  const Icon = listing.kind === "skill" ? Sparkles : Server;
  // A withdrawn row is a tombstone, not a deletion: the name stays reserved for
  // this workspace, so the row keeps offering "publish again" and nothing else.
  const withdrawn = listing.state === "withdrawn";

  return (
    <li className="flex items-start gap-3 px-4 py-3">
      <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate text-body font-medium">{listing.name}</span>
          <Badge variant="secondary">{kindLabel(listing.kind)}</Badge>
          <Badge variant={withdrawn ? "outline" : "secondary"}>
            {listing.state === "published"
              ? t(($) => $.marketplace.state_published)
              : withdrawn
                ? t(($) => $.marketplace.state_withdrawn)
                : // A state a newer backend introduced renders as itself
                  // instead of being mislabelled as one of the two we know.
                  listing.state}
          </Badge>
          {listing.transport ? (
            <Badge variant="outline">{listing.transport}</Badge>
          ) : null}
        </div>
        <p className="mt-0.5 text-caption text-muted-foreground">{listing.summary}</p>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {withdrawn ? (
          <Button size="sm" variant="outline" onClick={onRepublish}>
            {t(($) => $.marketplace.republish)}
          </Button>
        ) : (
          <>
            <Button size="sm" variant="ghost" onClick={onEdit}>
              {t(($) => $.marketplace.edit)}
            </Button>
            <Button size="sm" variant="outline" onClick={onWithdraw}>
              {t(($) => $.marketplace.withdraw)}
            </Button>
          </>
        )}
      </div>
    </li>
  );
}

/**
 * Pulls the secret-scan report out of a rejected publish.
 *
 * Only a 422 carries one, and only the location fields are read: taking the
 * whole body would risk rendering something the server did not intend to be
 * displayed. Anything else — a 409, a 403, a malformed body — returns null and
 * falls through to the plain error message.
 */
function scanErrorOf(
  error: unknown,
): { findings: MarketplaceScanFinding[]; scannerRevision: string } | null {
  if (!(error instanceof ApiError) || error.status !== 422) return null;
  const body = error.body;
  if (!body || typeof body !== "object") return null;
  const raw = (body as { findings?: unknown }).findings;
  if (!Array.isArray(raw)) return null;
  const findings = raw.flatMap((entry) => {
    if (!entry || typeof entry !== "object") return [];
    const finding = entry as Record<string, unknown>;
    return [
      {
        category: typeof finding.category === "string" ? finding.category : "",
        rule: typeof finding.rule === "string" ? finding.rule : "",
        field: typeof finding.field === "string" ? finding.field : "",
        line: typeof finding.line === "number" ? finding.line : 0,
        mask: typeof finding.mask === "string" ? finding.mask : "",
      },
    ];
  });
  if (findings.length === 0) return null;
  const revision = (body as { scanner_revision?: unknown }).scanner_revision;
  return {
    findings,
    scannerRevision: typeof revision === "string" ? revision : "",
  };
}

/**
 * `kind` is a server-driven string, so a kind added by a newer backend renders
 * as itself rather than disappearing from the listing.
 */
function kindLabel(kind: string): string {
  switch (kind) {
    case "skill":
      return "Skill";
    case "mcp":
      return "MCP";
    default:
      return kind || "unknown";
  }
}
