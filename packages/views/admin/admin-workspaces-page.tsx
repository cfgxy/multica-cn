"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Search, UserPlus } from "lucide-react";
import {
  adminWorkspacesOptions,
  useAdminAddWorkspaceMember,
} from "@multica/core/admin";
import type { AdminWorkspace } from "@multica/core/admin";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
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

function formatTimestamp(iso: string): string {
  if (iso === "") return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString();
}

export function AdminWorkspacesPage() {
  const { t } = useTranslation("admin");
  const [query, setQuery] = useState("");
  const { data, isLoading } = useQuery(adminWorkspacesOptions({ query }));

  const [addTarget, setAddTarget] = useState<AdminWorkspace | null>(null);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<"member" | "admin">("member");
  const [reason, setReason] = useState("");
  const [addError, setAddError] = useState(false);

  const addMember = useAdminAddWorkspaceMember();

  const closeDialog = () => {
    setAddTarget(null);
    setEmail("");
    setRole("member");
    setReason("");
    setAddError(false);
  };

  const submitAdd = async () => {
    if (addTarget === null || email.trim() === "") return;
    try {
      setAddError(false);
      await addMember.mutateAsync({
        workspaceId: addTarget.id,
        email: email.trim(),
        role,
        reason,
      });
      closeDialog();
    } catch {
      setAddError(true);
    }
  };

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
              <TableHead>{t(($) => $.workspaces.col_name)}</TableHead>
              <TableHead>{t(($) => $.workspaces.col_slug)}</TableHead>
              <TableHead>{t(($) => $.workspaces.col_owner)}</TableHead>
              <TableHead className="text-right">{t(($) => $.workspaces.col_members)}</TableHead>
              <TableHead>{t(($) => $.workspaces.col_created)}</TableHead>
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
            {isLoading === false && (data?.workspaces.length ?? 0) === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground">
                  {t(($) => $.empty)}
                </TableCell>
              </TableRow>
            )}
            {data?.workspaces.map((ws) => (
              <TableRow key={ws.id}>
                <TableCell className="text-body">{ws.name}</TableCell>
                <TableCell className="text-muted-foreground">{ws.slug}</TableCell>
                <TableCell>
                  {ws.owner_email !== null && ws.owner_email !== undefined
                    ? (
                      <div className="flex flex-col">
                        <span className="text-body">{ws.owner_name || ws.owner_email}</span>
                        <span className="text-caption text-muted-foreground">{ws.owner_email}</span>
                      </div>
                    )
                    : t(($) => $.workspaces.no_owner)}
                </TableCell>
                <TableCell className="text-right tabular-nums">{ws.member_count}</TableCell>
                <TableCell className="text-muted-foreground">{formatTimestamp(ws.created_at)}</TableCell>
                <TableCell>
                  <div className="flex justify-end">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setAddTarget(ws)}
                    >
                      <UserPlus className="size-4" aria-hidden />
                      {t(($) => $.workspaces.add_member)}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Dialog open={addTarget !== null} onOpenChange={(open) => open === false && closeDialog()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {addTarget !== null &&
                t(($) => $.workspaces.add_member_title, { name: addTarget.name })}
            </DialogTitle>
            <DialogDescription>
              {t(($) => $.subtitle)}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="admin-add-member-email">{t(($) => $.workspaces.email_label)}</Label>
              <Input
                id="admin-add-member-email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder={t(($) => $.workspaces.email_placeholder)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>{t(($) => $.workspaces.role_label)}</Label>
              <Select
                items={[
                  { value: "member", label: t(($) => $.workspaces.role_member) },
                  { value: "admin", label: t(($) => $.workspaces.role_admin) },
                ]}
                value={role}
                onValueChange={(v) => {
                  // Server contract (Q3=B): only member/admin are accepted.
                  if (v === "admin") setRole("admin");
                  else setRole("member");
                }}
              >
                <SelectTrigger aria-label={t(($) => $.workspaces.role_label)}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="member">{t(($) => $.workspaces.role_member)}</SelectItem>
                  <SelectItem value="admin">{t(($) => $.workspaces.role_admin)}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="admin-add-member-reason">{t(($) => $.users.reason_label)}</Label>
              <Input
                id="admin-add-member-reason"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder={t(($) => $.users.reason_placeholder)}
              />
            </div>
            {addError === true && (
              <p className="text-caption text-destructive">{t(($) => $.workspaces.add_failed)}</p>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={closeDialog}>
              {t(($) => $.users.cancel)}
            </Button>
            <Button
              disabled={email.trim() === "" || addMember.isPending === true}
              onClick={() => void submitAdd()}
            >
              {t(($) => $.workspaces.add)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
