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
import { useCurrentWorkspace } from "@multica/core/paths";
import { useCurrentMember } from "@multica/core/permissions";
import { pluginInstallationsOptions } from "@multica/core/plugins";
import {
  marketplaceItemsOptions,
  marketplaceListingsOptions,
} from "@multica/core/workspace/queries";
import {
  useInstallMarketplaceItem,
  useWithdrawMarketplaceListing,
} from "@multica/core/workspace/mutations";
import type { MarketplaceItem, MarketplaceListing } from "@multica/core/types";
import { useT } from "../../i18n";
import { PromptMarketPanel } from "../../market/prompt-market-panel";
import { MarketplaceInstallDialog } from "./marketplace-install-dialog";
import { PluginDirectory } from "./plugin-directory";
import { MarketplacePublishDialog } from "./marketplace-publish-dialog";
import { useMarketplacePublishFlow } from "./use-marketplace-publish";
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

  // The publish half is shared with the skill and MCP rows, which offer the
  // same flow for an entity the workspace already has. Only fetched when the
  // workspace can actually manage listings.
  const publishFlow = useMarketplacePublishFlow(wsId);
  const canPublish = publishFlow.canPublish;
  const listingsQuery = useQuery({
    ...marketplaceListingsOptions(wsId),
    enabled: wsId !== "" && canPublish,
  });
  const listings = useMemo(() => listingsQuery.data ?? [], [listingsQuery.data]);

  const withdraw = useWithdrawMarketplaceListing(wsId);
  const openPublishDialog = publishFlow.open;

  const [withdrawing, setWithdrawing] = useState<MarketplaceListing | null>(null);

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

      {/* Prompts are a second asset family on the same tab, not a variant of
          the skill/MCP catalog: they install into a workspace library and
          reach nothing until they are applied, so they get their own section
          rather than extra rows in the list above. */}
      <PromptMarketSection wsId={wsId} />

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

      <MarketplacePublishDialog {...publishFlow.dialogProps} />

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

/**
 * Wraps the prompt panel in the tab's own section chrome. Kept here rather
 * than inside `PromptMarketPanel` so the panel stays reusable outside the
 * settings page and carries no settings-layout dependency.
 */
function PromptMarketSection({ wsId }: { wsId: string }) {
  const { t } = useT("prompt-market");
  return (
    <SettingsSection
      title={t(($) => $.section.title)}
      description={t(($) => $.section.description)}
    >
      <PromptMarketPanel wsId={wsId} />
    </SettingsSection>
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
