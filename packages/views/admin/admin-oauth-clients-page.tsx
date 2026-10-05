"use client";

// Admin → OAuth 客户端（RUYI-420）。注册/编辑 MCP OAuth 客户端、轮换与
// 禁用、用户授权（grant）目录与撤销。明文 secret 只在创建/轮换的一次性
// 弹窗里出现，关闭即不可再见——列表、详情、审计永远不含 hash 或明文。

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Plus, Copy, Check } from "lucide-react";
import {
  adminOAuthClientsOptions,
  adminOAuthGrantsOptions,
  useAdminCreateOAuthClient,
  useAdminUpdateOAuthClient,
  useAdminSetOAuthClientDisabled,
  useAdminRotateOAuthClientSecret,
  useAdminDeleteOAuthClient,
  useAdminRevokeOAuthGrant,
} from "@multica/core/oauth-admin";
import type { AdminOAuthClient } from "@multica/core/oauth-admin";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
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
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
import { copyText } from "@multica/ui/lib/clipboard";

type PendingAction =
  | { kind: "rotate"; target: AdminOAuthClient }
  | { kind: "disable"; target: AdminOAuthClient }
  | { kind: "enable"; target: AdminOAuthClient }
  | { kind: "delete"; target: AdminOAuthClient }
  | { kind: "revoke-grant"; grantId: string; label: string };

function formatTimestamp(iso: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

/** One URI per line in the editor; empty lines dropped on save. */
function splitRedirectURIs(raw: string): string[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

export function AdminOAuthClientsPage() {
  const { t } = useTranslation("admin");
  const clientsQuery = useQuery(adminOAuthClientsOptions());
  const grantsQuery = useQuery(adminOAuthGrantsOptions());

  const createClient = useAdminCreateOAuthClient();
  const updateClient = useAdminUpdateOAuthClient();
  const setDisabled = useAdminSetOAuthClientDisabled();
  const rotateSecret = useAdminRotateOAuthClientSecret();
  const deleteClient = useAdminDeleteOAuthClient();
  const revokeGrant = useAdminRevokeOAuthGrant();

  // Create dialog state.
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createURIs, setCreateURIs] = useState("");
  const [createError, setCreateError] = useState("");

  // Edit dialog state (client being edited, null = closed).
  const [editTarget, setEditTarget] = useState<AdminOAuthClient | null>(null);
  const [editName, setEditName] = useState("");
  const [editURIs, setEditURIs] = useState("");
  const [editError, setEditError] = useState("");

  // One-shot secret reveal (create or rotate result).
  const [reveal, setReveal] = useState<{ secret: string; kind: "create" | "rotate" } | null>(null);
  const [secretCopied, setSecretCopied] = useState(false);

  // Confirmation dialogs share one pending slot.
  const [pending, setPending] = useState<PendingAction | null>(null);
  const [reason, setReason] = useState("");

  const openCreate = () => {
    setCreateName("");
    setCreateURIs("");
    setCreateError("");
    setCreateOpen(true);
  };

  const submitCreate = async () => {
    setCreateError("");
    try {
      const result = await createClient.mutateAsync({
        name: createName.trim(),
        redirect_uris: splitRedirectURIs(createURIs),
      });
      setCreateOpen(false);
      setSecretCopied(false);
      setReveal({ secret: result.secret, kind: "create" });
    } catch (error) {
      setCreateError(error instanceof Error ? error.message : String(error));
    }
  };

  const openEdit = (client: AdminOAuthClient) => {
    setEditName(client.name);
    setEditURIs(client.redirect_uris.join("\n"));
    setEditError("");
    setEditTarget(client);
  };

  const submitEdit = async () => {
    if (editTarget === null) return;
    setEditError("");
    try {
      await updateClient.mutateAsync({
        id: editTarget.id,
        name: editName.trim(),
        redirect_uris: splitRedirectURIs(editURIs),
      });
      setEditTarget(null);
    } catch (error) {
      setEditError(error instanceof Error ? error.message : String(error));
    }
  };

  const runPending = async () => {
    if (pending === null) return;
    const action = pending;
    setPending(null);
    setReason("");
    try {
      if (action.kind === "rotate") {
        const result = await rotateSecret.mutateAsync({ id: action.target.id, reason });
        setSecretCopied(false);
        setReveal({ secret: result.secret, kind: "rotate" });
      } else if (action.kind === "disable") {
        await setDisabled.mutateAsync({ id: action.target.id, disabled: true, reason });
      } else if (action.kind === "enable") {
        await setDisabled.mutateAsync({ id: action.target.id, disabled: false, reason });
      } else if (action.kind === "delete") {
        await deleteClient.mutateAsync({ id: action.target.id, reason });
      } else if (action.kind === "revoke-grant") {
        await revokeGrant.mutateAsync({ id: action.grantId, reason });
      }
    } catch {
      // Confirmation dialogs guard destructive actions; server-side
      // failures surface through the invalidated directory refetch.
    }
  };

  const pendingBody = (() => {
    if (pending === null) return "";
    const name = "target" in pending ? pending.target.name : pending.label;
    switch (pending.kind) {
      case "rotate":
        return t(($) => $.oauth.rotate_confirm_body, { name });
      case "disable":
        return t(($) => $.oauth.disable_confirm_body, { name });
      case "enable":
        return t(($) => $.oauth.enable_confirm_body, { name });
      case "delete":
        return t(($) => $.oauth.delete_confirm_body, { name });
      case "revoke-grant":
        return t(($) => $.oauth.revoke_grant_confirm_body, { name });
    }
  })();

  const clients = clientsQuery.data?.clients ?? [];
  const grants = grantsQuery.data?.grants ?? [];

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center justify-between gap-2 px-4 py-3">
        <span className="text-caption text-muted-foreground">
          {t(($) => $.oauth.clients_hint)}
        </span>
        <Button size="sm" onClick={openCreate}>
          <Plus className="size-4" aria-hidden />
          {t(($) => $.oauth.create)}
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-4 pb-6">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t(($) => $.oauth.col_name)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_client_id)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_status)}</TableHead>
              <TableHead className="text-right">{t(($) => $.oauth.col_grants)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_last_used)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_secret_updated)}</TableHead>
              <TableHead className="w-px" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {clientsQuery.isLoading === true && (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  {t(($) => $.loading)}
                </TableCell>
              </TableRow>
            )}
            {clientsQuery.isLoading === false && clients.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  {t(($) => $.oauth.empty)}
                </TableCell>
              </TableRow>
            )}
            {clients.map((client) => {
              const disabled = client.disabled_at !== null;
              return (
                <TableRow key={client.id} data-disabled={disabled || undefined}>
                  <TableCell>
                    <div className="flex flex-col">
                      <span className="text-body">{client.name}</span>
                      <span className="max-w-72 truncate text-caption text-muted-foreground" title={client.redirect_uris.join("\n")}>
                        {client.redirect_uris.join(", ")}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-caption">{client.client_id}</TableCell>
                  <TableCell>
                    {disabled === true
                      ? <Badge variant="secondary">{t(($) => $.oauth.status_disabled)}</Badge>
                      : <Badge variant="outline">{t(($) => $.oauth.status_active)}</Badge>}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {client.active_grants}/{client.grant_count}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{formatTimestamp(client.last_used_at)}</TableCell>
                  <TableCell className="text-muted-foreground">{formatTimestamp(client.secret_updated_at)}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" onClick={() => openEdit(client)}>
                        {t(($) => $.oauth.edit)}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => setPending({ kind: "rotate", target: client })}>
                        {t(($) => $.oauth.rotate)}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          setPending(disabled === true
                            ? { kind: "enable", target: client }
                            : { kind: "disable", target: client })}
                      >
                        {disabled === true ? t(($) => $.oauth.enable) : t(($) => $.oauth.disable)}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => setPending({ kind: "delete", target: client })}>
                        {t(($) => $.oauth.delete)}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>

        <h3 className="px-0 pb-2 pt-6 text-body font-semibold">
          {t(($) => $.oauth.grants_title)}
        </h3>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t(($) => $.oauth.col_user)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_client)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_scope)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_authorized_at)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_last_used)}</TableHead>
              <TableHead>{t(($) => $.oauth.col_status)}</TableHead>
              <TableHead className="w-px" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {grantsQuery.isLoading === true && (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  {t(($) => $.loading)}
                </TableCell>
              </TableRow>
            )}
            {grantsQuery.isLoading === false && grants.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="text-center text-muted-foreground">
                  {t(($) => $.oauth.grants_empty)}
                </TableCell>
              </TableRow>
            )}
            {grants.map((grant) => {
              const revoked = grant.revoked_at !== null;
              const userLabel = grant.user_name || grant.user_email || grant.user_id;
              return (
                <TableRow key={grant.id} data-revoked={revoked || undefined}>
                  <TableCell>
                    <div className="flex flex-col">
                      <span className="text-body">{userLabel}</span>
                      {grant.user_email !== null && grant.user_email !== userLabel && (
                        <span className="text-caption text-muted-foreground">{grant.user_email}</span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col">
                      <span>{grant.client_name ?? grant.client_id}</span>
                      <span className="font-mono text-caption text-muted-foreground">{grant.client_id}</span>
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-caption">{grant.scope}</TableCell>
                  <TableCell className="text-muted-foreground">{formatTimestamp(grant.created_at)}</TableCell>
                  <TableCell className="text-muted-foreground">{formatTimestamp(grant.last_used_at)}</TableCell>
                  <TableCell>
                    {revoked === true
                      ? <Badge variant="secondary">{t(($) => $.oauth.status_revoked)}</Badge>
                      : <Badge variant="outline">{t(($) => $.oauth.status_active)}</Badge>}
                  </TableCell>
                  <TableCell>
                    {revoked === false && (
                      <div className="flex justify-end">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() =>
                            setPending({
                              kind: "revoke-grant",
                              grantId: grant.id,
                              label: `${userLabel} · ${grant.client_name ?? grant.client_id}`,
                            })}
                        >
                          {t(($) => $.oauth.revoke_grant)}
                        </Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      {/* Create dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.oauth.create_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.oauth.create_description)}</DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="oauth-create-name">{t(($) => $.oauth.name_label)}</Label>
              <Input
                id="oauth-create-name"
                value={createName}
                onChange={(e) => setCreateName(e.target.value)}
                placeholder={t(($) => $.oauth.name_placeholder)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="oauth-create-uris">{t(($) => $.oauth.redirect_uris_label)}</Label>
              <Textarea
                id="oauth-create-uris"
                value={createURIs}
                onChange={(e) => setCreateURIs(e.target.value)}
                placeholder={"https://chatgpt.com/connector_platform_oauth_redirect"}
                rows={3}
              />
              <p className="text-caption text-muted-foreground">{t(($) => $.oauth.redirect_uris_hint)}</p>
            </div>
            {createError !== "" && (
              <p className="text-caption text-destructive">{createError}</p>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              {t(($) => $.oauth.cancel)}
            </Button>
            <Button disabled={createClient.isPending} onClick={() => void submitCreate()}>
              {t(($) => $.oauth.create)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit dialog */}
      <Dialog open={editTarget !== null} onOpenChange={(open) => { if (open === false) setEditTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.oauth.edit_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.oauth.edit_description, { client_id: editTarget?.client_id ?? "" })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="oauth-edit-name">{t(($) => $.oauth.name_label)}</Label>
              <Input
                id="oauth-edit-name"
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="oauth-edit-uris">{t(($) => $.oauth.redirect_uris_label)}</Label>
              <Textarea
                id="oauth-edit-uris"
                value={editURIs}
                onChange={(e) => setEditURIs(e.target.value)}
                rows={3}
              />
            </div>
            {editError !== "" && (
              <p className="text-caption text-destructive">{editError}</p>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditTarget(null)}>
              {t(($) => $.oauth.cancel)}
            </Button>
            <Button disabled={updateClient.isPending} onClick={() => void submitEdit()}>
              {t(($) => $.oauth.save)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* One-shot secret reveal (create / rotate) */}
      <Dialog open={reveal !== null} onOpenChange={(open) => { if (open === false) setReveal(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {reveal?.kind === "create"
                ? t(($) => $.oauth.secret_created_title)
                : t(($) => $.oauth.secret_rotated_title)}
            </DialogTitle>
            <DialogDescription>{t(($) => $.oauth.secret_once_warning)}</DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 break-all rounded bg-muted px-2 py-1.5 font-mono text-caption select-all">
              {reveal?.secret ?? ""}
            </code>
            <Button
              variant="outline"
              size="icon"
              aria-label={t(($) => $.oauth.copy_secret)}
              onClick={() => {
                if (reveal === null) return;
                void copyText(reveal.secret);
                setSecretCopied(true);
              }}
            >
              {secretCopied === true ? <Check className="size-4" /> : <Copy className="size-4" />}
            </Button>
          </div>
          <DialogFooter>
            <Button onClick={() => setReveal(null)}>{t(($) => $.oauth.secret_done)}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Confirmation dialog (rotate / disable / enable / delete / revoke) */}
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
            <AlertDialogTitle>
              {pending?.kind === "rotate" && t(($) => $.oauth.rotate_confirm_title)}
              {pending?.kind === "disable" && t(($) => $.oauth.disable_confirm_title)}
              {pending?.kind === "enable" && t(($) => $.oauth.enable_confirm_title)}
              {pending?.kind === "delete" && t(($) => $.oauth.delete_confirm_title)}
              {pending?.kind === "revoke-grant" && t(($) => $.oauth.revoke_grant_confirm_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>{pendingBody}</AlertDialogDescription>
          </AlertDialogHeader>
          <Input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t(($) => $.oauth.reason_placeholder)}
            aria-label={t(($) => $.oauth.reason_label)}
          />
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.oauth.cancel)}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void runPending()}>
              {t(($) => $.oauth.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
