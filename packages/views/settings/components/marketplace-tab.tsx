"use client";

import { useMemo, useState } from "react";
import { Check, Loader2, Search, Server, Sparkles, Store } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useCurrentMember } from "@multica/core/permissions";
import { marketplaceItemsOptions } from "@multica/core/workspace/queries";
import { useInstallMarketplaceItem } from "@multica/core/workspace/mutations";
import type { MarketplaceItem } from "@multica/core/types";
import { useT } from "../../i18n";
import { MarketplaceInstallDialog } from "./marketplace-install-dialog";
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
  const itemsQuery = useQuery(marketplaceItemsOptions(wsId, { kind, q: search }));
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
      <SettingsSection
        title={t(($) => $.marketplace.browse_title)}
        description={t(($) => $.marketplace.binding_note)}
      >
        <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center">
          <Tabs value={kind} onValueChange={setKind}>
            <TabsList>
              <TabsTrigger value="">{t(($) => $.marketplace.filter_all)}</TabsTrigger>
              <TabsTrigger value="skill">{t(($) => $.marketplace.filter_skills)}</TabsTrigger>
              <TabsTrigger value="mcp">{t(($) => $.marketplace.filter_mcp)}</TabsTrigger>
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
