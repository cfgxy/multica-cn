"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Loader2, Search, Store } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { useCurrentMember } from "@multica/core/permissions";
import {
  promptInstallsOptions,
  promptMarketOptions,
} from "@multica/core/workspace/queries";
import {
  useInstallPrompt,
  useWithdrawPromptVersion,
} from "@multica/core/workspace/mutations";
import type { PromptInstall, PromptMarketItem } from "@multica/core/types";
import { useT } from "../i18n";
import { PromptApplyDialog } from "./prompt-apply-dialog";
import { PromptWithdrawDialog } from "./prompt-confirm-dialogs";
import { PromptDetailDialog } from "./prompt-detail-dialog";
import { PromptInstallDialog } from "./prompt-install-dialog";
import { licenseLabel, promptKindLabel } from "./prompt-market-labels";

type Segment = "market" | "installed";

/**
 * The prompt half of the marketplace settings tab: a cross-workspace catalog
 * and this workspace's install library, kept as two separate segments.
 *
 * The split is the two-phase design made visible. "Market" is where a version
 * enters the library and "Installed" is where a library row reaches an agent
 * or squad, so no single click can take a prompt from a stranger's workspace
 * into a running agent.
 *
 * The catalog itself is member-visible — the listing carries metadata only and
 * never the prompt body. Installing is owner/admin, enforced server-side; the
 * button is hidden for a member, and a note says why rather than leaving a
 * dead row.
 */
export function PromptMarketPanel({ wsId }: { wsId: string }) {
  const { t } = useT("prompt-market");
  const currentMember = useCurrentMember(wsId);
  const canInstall =
    currentMember.role === "owner" || currentMember.role === "admin";

  const [segment, setSegment] = useState<Segment>("market");
  const [kind, setKind] = useState("");
  const [search, setSearch] = useState("");

  const catalog = useQuery({
    ...promptMarketOptions(wsId, { kind, q: search }),
    enabled: segment === "market" && wsId !== "",
  });
  const installs = useQuery({
    ...promptInstallsOptions(wsId),
    enabled: segment === "installed" && wsId !== "",
  });

  const install = useInstallPrompt(wsId);
  const withdraw = useWithdrawPromptVersion(wsId);

  const [detailItem, setDetailItem] = useState<PromptMarketItem | null>(null);
  const [installItem, setInstallItem] = useState<PromptMarketItem | null>(null);
  const [withdrawItem, setWithdrawItem] = useState<PromptMarketItem | null>(null);
  const [applyInstall, setApplyInstall] = useState<PromptInstall | null>(null);

  const items = useMemo(() => catalog.data ?? [], [catalog.data]);
  const installRows = useMemo(() => installs.data ?? [], [installs.data]);

  const runInstall = async () => {
    if (!installItem) return;
    try {
      await install.mutateAsync(installItem.id);
      toast.success(t(($) => $.install.installed_toast, { name: installItem.name }));
      setInstallItem(null);
      setDetailItem(null);
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.install.failed_toast),
      );
    }
  };

  const runWithdraw = async () => {
    if (!withdrawItem) return;
    try {
      await withdraw.mutateAsync(withdrawItem.id);
      toast.success(t(($) => $.withdraw.done_toast));
      setWithdrawItem(null);
      setDetailItem(null);
    } catch (error) {
      toast.error(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.withdraw.failed_toast),
      );
    }
  };

  return (
    <div className="space-y-3">
      <Tabs value={segment} onValueChange={(value) => setSegment(value as Segment)}>
        <TabsList>
          <TabsTrigger value="market">{t(($) => $.segment.market)}</TabsTrigger>
          <TabsTrigger value="installed">{t(($) => $.segment.installed)}</TabsTrigger>
        </TabsList>
      </Tabs>

      {segment === "market" ? (
        <>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <Tabs value={kind} onValueChange={setKind}>
              <TabsList>
                <TabsTrigger value="">{t(($) => $.filter.all)}</TabsTrigger>
                <TabsTrigger value="agent_prompt">
                  {t(($) => $.filter.agent_prompt)}
                </TabsTrigger>
                <TabsTrigger value="squad_prompt">
                  {t(($) => $.filter.squad_prompt)}
                </TabsTrigger>
              </TabsList>
            </Tabs>
            <div className="relative flex-1">
              <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                className="pl-8"
                value={search}
                placeholder={t(($) => $.search_placeholder)}
                onChange={(event) => setSearch(event.target.value)}
              />
            </div>
          </div>

          <div className="rounded-lg border border-surface-border">
            {catalog.isLoading ? (
              <Spinner />
            ) : items.length === 0 ? (
              <EmptyState
                title={t(($) => $.list.empty_title)}
                description={t(($) => $.list.empty_description)}
              />
            ) : (
              <ul className="divide-y divide-surface-border">
                {items.map((item) => (
                  <li key={item.id} className="flex items-start gap-3 px-4 py-3">
                    <div className="min-w-0 flex-1">
                      <div className="flex min-w-0 flex-wrap items-center gap-2">
                        <span className="truncate text-body font-medium">
                          {item.name}
                        </span>
                        <Badge variant="secondary">
                          {promptKindLabel(t, item.kind)}
                        </Badge>
                        {item.version !== null ? (
                          <Badge variant="outline">
                            {t(($) => $.badge.version, { version: item.version })}
                          </Badge>
                        ) : null}
                        {item.installed ? (
                          <Badge variant="outline">
                            <Check className="h-3 w-3" />
                            {t(($) => $.badge.installed)}
                          </Badge>
                        ) : null}
                        {item.update_available && item.version !== null ? (
                          <Badge>
                            {t(($) => $.badge.update_available, {
                              version: item.version,
                            })}
                          </Badge>
                        ) : null}
                      </div>
                      <p className="mt-0.5 text-caption text-muted-foreground">
                        {item.summary}
                      </p>
                      <p className="mt-0.5 text-caption text-muted-foreground">
                        {item.publisher_display_name}
                        {" · "}
                        {licenseLabel(t, item.license_code)}
                      </p>
                    </div>
                    <Button
                      size="sm"
                      variant="outline"
                      className="shrink-0"
                      onClick={() => setDetailItem(item)}
                    >
                      {t(($) => $.list.details)}
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {!canInstall && !currentMember.isLoading ? (
            <p className="px-0.5 text-caption text-muted-foreground">
              {t(($) => $.list.member_note)}
            </p>
          ) : null}
        </>
      ) : (
        <div className="rounded-lg border border-surface-border">
          {installs.isLoading ? (
            <Spinner />
          ) : installRows.length === 0 ? (
            <EmptyState
              title={t(($) => $.list.installed_empty_title)}
              description={t(($) => $.list.installed_empty_description)}
            />
          ) : (
            <ul className="divide-y divide-surface-border">
              {installRows.map((row) => (
                <li key={row.id} className="flex items-start gap-3 px-4 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="truncate text-body font-medium">{row.name}</span>
                      <Badge variant="secondary">{promptKindLabel(t, row.kind)}</Badge>
                      <Badge variant="outline">
                        {t(($) => $.badge.version, { version: row.installed_version })}
                      </Badge>
                    </div>
                    <p className="mt-0.5 text-caption text-muted-foreground">
                      {row.summary}
                    </p>
                  </div>
                  {canInstall ? (
                    <Button
                      size="sm"
                      className="shrink-0"
                      onClick={() => setApplyInstall(row)}
                    >
                      {t(($) => $.apply.cta)}
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <PromptDetailDialog
        open={detailItem !== null}
        wsId={wsId}
        item={detailItem}
        canInstall={canInstall}
        onOpenChange={(open) => {
          if (!open) setDetailItem(null);
        }}
        onInstall={setInstallItem}
        onWithdraw={setWithdrawItem}
      />
      <PromptInstallDialog
        open={installItem !== null}
        item={installItem}
        installing={install.isPending}
        onOpenChange={(open) => {
          if (!open) setInstallItem(null);
        }}
        onConfirm={() => void runInstall()}
      />
      <PromptWithdrawDialog
        open={withdrawItem !== null}
        version={withdrawItem?.version ?? null}
        pending={withdraw.isPending}
        onOpenChange={(open) => {
          if (!open) setWithdrawItem(null);
        }}
        onConfirm={() => void runWithdraw()}
      />
      <PromptApplyDialog
        open={applyInstall !== null}
        wsId={wsId}
        install={applyInstall}
        onOpenChange={(open) => {
          if (!open) setApplyInstall(null);
        }}
      />
    </div>
  );
}

function Spinner() {
  return (
    <div className="flex items-center justify-center py-8 text-muted-foreground">
      <Loader2 className="h-4 w-4 animate-spin" />
    </div>
  );
}

function EmptyState({ title, description }: { title: string; description: string }) {
  return (
    <div className="px-4 py-8 text-center">
      <Store className="mx-auto h-5 w-5 text-muted-foreground" />
      <p className="mt-3 text-body font-medium">{title}</p>
      <p className="mx-auto mt-1 max-w-md text-caption leading-5 text-muted-foreground">
        {description}
      </p>
    </div>
  );
}
