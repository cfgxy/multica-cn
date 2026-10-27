"use client";

// Settings → 我的授权（RUYI-420）。当前用户对 MCP OAuth 客户端的授权记录
// 自查与自撤；管理员侧的客户端与全量 grants 管理在 /admin。

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { myOAuthGrantsOptions, useRevokeMyOAuthGrant } from "@multica/core/oauth-admin";
import type { MyOAuthGrant } from "@multica/core/oauth-admin";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
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
import { useLocale, useT } from "../../i18n";
import { SettingsSection, SettingsTab } from "./settings-layout";

function formatDateTime(value: string | null, locale: string): string | null {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function GrantRow({ grant, onRevoke }: { grant: MyOAuthGrant; onRevoke: (grant: MyOAuthGrant) => void }) {
  const { t } = useT("settings");
  const locale = useLocale();
  const revoked = grant.revoked_at !== null;

  return (
    <TableRow>
      <TableCell>
        <div className="font-medium">{grant.client_name || grant.client_id}</div>
        <div className="font-mono text-caption text-muted-foreground">{grant.client_id}</div>
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap gap-1">
          {grant.scope.split(/[\s,]+/).filter(Boolean).map((s) => (
            <Badge key={s} variant="outline" className="font-mono">{s}</Badge>
          ))}
        </div>
      </TableCell>
      <TableCell className="text-caption text-muted-foreground">
        {formatDateTime(grant.created_at, locale) ?? "—"}
      </TableCell>
      <TableCell className="text-caption text-muted-foreground">
        {formatDateTime(grant.last_used_at, locale) ?? "—"}
      </TableCell>
      <TableCell>
        {revoked
          ? <Badge variant="secondary">{t(($) => $.authorizations.status_revoked)}</Badge>
          : <Badge variant="default">{t(($) => $.authorizations.status_active)}</Badge>}
      </TableCell>
      <TableCell className="text-right">
        <Button
          variant="outline"
          size="sm"
          disabled={revoked}
          onClick={() => onRevoke(grant)}
        >
          {t(($) => $.authorizations.revoke)}
        </Button>
      </TableCell>
    </TableRow>
  );
}

export function AuthorizationsTab() {
  const { t } = useT("settings");
  const { data, isLoading } = useQuery(myOAuthGrantsOptions());
  const revokeGrant = useRevokeMyOAuthGrant();
  const [pending, setPending] = useState<MyOAuthGrant | null>(null);

  const grants = data?.grants ?? [];

  return (
    <SettingsTab
      title={t(($) => $.authorizations.title)}
      description={t(($) => $.authorizations.description)}
    >
      <SettingsSection title={t(($) => $.authorizations.list_title)}>
        {isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : grants.length === 0 ? (
          <p className="py-6 text-center text-caption text-muted-foreground">
            {t(($) => $.authorizations.empty)}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t(($) => $.authorizations.col_client)}</TableHead>
                <TableHead>{t(($) => $.authorizations.col_scope)}</TableHead>
                <TableHead>{t(($) => $.authorizations.col_authorized_at)}</TableHead>
                <TableHead>{t(($) => $.authorizations.col_last_used)}</TableHead>
                <TableHead>{t(($) => $.authorizations.col_status)}</TableHead>
                <TableHead className="text-right">{""}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {grants.map((grant) => (
                <GrantRow key={grant.id} grant={grant} onRevoke={setPending} />
              ))}
            </TableBody>
          </Table>
        )}
      </SettingsSection>

      <AlertDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.authorizations.revoke_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.authorizations.revoke_confirm_body, {
                client: pending?.client_name || pending?.client_id || "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.authorizations.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              disabled={revokeGrant.isPending}
              onClick={() => {
                if (pending === null) return;
                revokeGrant.mutate(pending.id, {
                  onSettled: () => setPending(null),
                });
              }}
            >
              {t(($) => $.authorizations.revoke)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsTab>
  );
}
