"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Search } from "lucide-react";
import {
  adminUsersOptions,
  useAdminSetUserDisabled,
  useAdminSetUserSuperAdmin,
} from "@multica/core/admin";
import { api } from "@multica/core/api";
import type { AdminUser } from "@multica/core/admin";
import { useAuthStore } from "@multica/core/auth";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
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
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";

type PendingAction =
  | { kind: "disable"; target: AdminUser }
  | { kind: "enable"; target: AdminUser }
  | { kind: "revoke"; target: AdminUser }
  | { kind: "impersonate"; target: AdminUser };

function formatTimestamp(iso: string): string {
  if (iso === "") return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString();
}

export function AdminUsersPage() {
  const { t } = useTranslation("admin");
  const me = useAuthStore((s) => s.user);
  const applySession = useAuthStore((s) => s.applySession);
  const [query, setQuery] = useState("");
  const [pending, setPending] = useState<PendingAction | null>(null);
  const [reason, setReason] = useState("");

  const { data, isLoading } = useQuery(adminUsersOptions({ query }));

  const setDisabled = useAdminSetUserDisabled();
  const setSuperAdmin = useAdminSetUserSuperAdmin();

  const runPending = async () => {
    if (pending === null) return;
    const { kind, target } = pending;
    setPending(null);
    setReason("");
    try {
      if (kind === "disable") {
        await setDisabled.mutateAsync({ userId: target.id, disabled: true, reason });
      } else if (kind === "enable") {
        await setDisabled.mutateAsync({ userId: target.id, disabled: false, reason });
      } else if (kind === "revoke") {
        await setSuperAdmin.mutateAsync({ userId: target.id, granted: false, reason });
      } else if (kind === "impersonate") {
        // Start: the server mints a shadow JWT (browser cookie or explicit
        // token) and the response IS the new session.
        const { token, user } = await api.adminImpersonate(target.id, reason);
        applySession(token, user);
      }
    } catch {
      // Row stays stale; the directory refetches on the next mutation
      // success. Server errors (403/409 guards) surface as no-ops here by
      // design — the buttons for guarded actions are disabled upfront.
    }
  };

  const isMe = (u: AdminUser) => me !== null && u.id === me.id;
  // Can-revoke needs at least one OTHER active super admin — the server
  // enforces this precisely; the UI preempts the obvious case (target is
  // the only visible active admin) without pretending to know the count.
  // Selector-API form needs statically-resolvable keys, so the three
  // confirmation dialogs branch before translating.
  let pendingTitle = "";
  let pendingBody = "";
  if (pending !== null) {
    const name = pending.target.name || pending.target.email;
    if (pending.kind === "disable") {
      pendingTitle = t(($) => $.users.disable_confirm_title, { name });
      pendingBody = t(($) => $.users.disable_confirm_body);
    } else if (pending.kind === "revoke") {
      pendingTitle = t(($) => $.users.revoke_confirm_title, { name });
      pendingBody = t(($) => $.users.revoke_confirm_body);
    } else {
      pendingTitle = t(($) => $.users.impersonate_confirm_title, { name });
      pendingBody = t(($) => $.users.impersonate_confirm_body);
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-2 px-4 py-3">
        <div className="relative w-72">
          <Search className="absolute left-2 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t(($) => $.search_placeholder)}
            className="pl-8"
            aria-label={t(($) => $.search_placeholder)}
          />
        </div>
        {data !== undefined && (
          <span className="text-caption text-muted-foreground">
            {t(($) => $.total, { count: data.total })}
          </span>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-4 pb-6">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t(($) => $.users.col_name)}</TableHead>
              <TableHead>{t(($) => $.users.col_status)}</TableHead>
              <TableHead>{t(($) => $.users.col_role)}</TableHead>
              <TableHead className="text-right">{t(($) => $.users.col_workspaces)}</TableHead>
              <TableHead>{t(($) => $.users.col_created)}</TableHead>
              <TableHead className="w-px" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading === true && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  {t(($) => $.loading)}
                </TableCell>
              </TableRow>
            )}
            {isLoading === false && (data?.users.length ?? 0) === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  {t(($) => $.empty)}
                </TableCell>
              </TableRow>
            )}
            {data?.users.map((u) => {
              const disabled = u.disabled_at !== null && u.disabled_at !== undefined;
              return (
                <TableRow key={u.id} data-disabled={disabled || undefined}>
                  <TableCell>
                    <div className="flex flex-col">
                      <span className="text-body">{u.name || u.email}</span>
                      <span className="text-caption text-muted-foreground">{u.email}</span>
                    </div>
                  </TableCell>
                  <TableCell>
                    {disabled === true
                      ? (
                        <Badge variant="secondary">
                          {t(($) => $.users.status_disabled)}
                        </Badge>
                      )
                      : (
                        <Badge variant="outline">{t(($) => $.users.status_active)}</Badge>
                      )}
                  </TableCell>
                  <TableCell>
                    {u.is_super_admin === true && (
                      <Badge>{t(($) => $.users.role_super_admin)}</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{u.workspace_count}</TableCell>
                  <TableCell className="text-muted-foreground">{formatTimestamp(u.created_at)}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      {u.is_super_admin === true
                        ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={isMe(u)}
                            onClick={() => setPending({ kind: "revoke", target: u })}
                          >
                            {t(($) => $.users.revoke)}
                          </Button>
                        )
                        : (
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={disabled}
                            onClick={() =>
                              void setSuperAdmin.mutateAsync({
                                userId: u.id,
                                granted: true,
                              }).catch(() => {})}
                          >
                            {t(($) => $.users.grant)}
                          </Button>
                        )}
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={isMe(u)}
                        onClick={() =>
                          setPending(disabled === true
                            ? { kind: "enable", target: u }
                            : { kind: "disable", target: u })}
                      >
                        {disabled === true ? t(($) => $.users.enable) : t(($) => $.users.disable)}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={disabled || u.is_super_admin === true || isMe(u)}
                        onClick={() => setPending({ kind: "impersonate", target: u })}
                      >
                        {t(($) => $.users.impersonate)}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      <AlertDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (open === false) {
            setPending(null);
            setReason("");
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{pendingTitle}</AlertDialogTitle>
            <AlertDialogDescription>{pendingBody}</AlertDialogDescription>
          </AlertDialogHeader>
          <Input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t(($) => $.users.reason_placeholder)}
            aria-label={t(($) => $.users.reason_label)}
          />
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.users.cancel)}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void runPending()}>
              {t(($) => $.users.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
