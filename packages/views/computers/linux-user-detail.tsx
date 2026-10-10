"use client";

import { useState, type ReactNode } from "react";
import {
  bindingSummary,
  useLinuxUserDetail,
  type ComputerLifecycleInput,
} from "@multica/core/computers";
import { AppLink } from "../navigation";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { LinuxUserStatus, LinuxUserTime } from "./linux-user-table-parts";
import { LinuxUserOperationsTable } from "./linux-user-operations-table";
import { Input } from "@multica/ui/components/ui/input";
import { LinuxPasswordInput } from "./linux-password-input";
import { useT } from "../i18n";

export function LinuxUserDetail({
  userId,
  bindingId,
  admin = false,
  onConfigure,
  children,
  view = "all",
  operationId,
  operationsHref,
}: {
  userId: string;
  bindingId: string;
  admin?: boolean;
  view?: "all" | "overview" | "operations";
  operationId?: string | null;
  operationsHref?: string;
  onConfigure?: (action: "create_account" | "upgrade") => void;
  children?: (busy: boolean) => ReactNode;
}) {
  const { t } = useT("settings");
  const data = useLinuxUserDetail(userId, bindingId, admin);
  const [action, setAction] = useState<ComputerLifecycleInput["action"] | null>(
    null,
  );
  const [confirmation, setConfirmation] = useState("");
  const [password, setPassword] = useState("");
  const d = data.detail.data;
  const busy =
    data.busy ||
    data.lifecycle.isPending ||
    data.discover.isPending ||
    d?.state === "running" ||
    d?.operation_busy === true ||
    d?.state === "interrupted";
  const error =
    data.detail.error ||
    data.operations.error ||
    data.lifecycle.error ||
    data.discover.error ||
    data.recover.error ||
    data.check.error;
  const states = t(($) => $.linux_user.states, { returnObjects: true });
  const hints = t(($) => $.linux_user_pages.hints, { returnObjects: true });
  const kinds = t(($) => $.linux_user.kinds, { returnObjects: true });
  const stateLabel = (state: string) =>
    states[state as keyof typeof states] ?? t(($) => $.linux_user.unknown);
  return (
    <section
      className="mt-3 space-y-3"
      aria-label={t(($) => $.linux_user.details)}
    >
      {error && (
        <p role="alert" className="text-destructive">
          {error.message}
        </p>
      )}
      {data.detail.isPending && (
        <p role="status">{t(($) => $.computers.loading)}</p>
      )}
      {d && view === "overview" && (
        <>
          <p className="max-w-2xl text-body text-muted-foreground">
            {hints[bindingSummary(d) as keyof typeof hints] ?? hints.unknown}
          </p>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h3 className="text-body font-semibold">
              {t(($) => $.linux_user_pages.tables.health)}
            </h3>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={data.check.isPending || busy}
                aria-busy={data.check.isPending}
                onClick={() => data.check.mutate()}
              >
                {t(($) => $.admin.linux_user_check)}
              </Button>
              {!admin &&
                !d.archived_at &&
                ["ready", "failed", "pending"].includes(d.state) && (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={busy}
                    aria-busy={data.discover.isPending}
                    onClick={() => data.discover.mutate()}
                  >
                    {t(($) => $.linux_user.discover)}
                  </Button>
                )}
            </div>
          </div>
          <div className="overflow-hidden rounded-lg border bg-card">
            <Table
              aria-label={t(($) => $.linux_user_pages.tables.health)}
              className="min-w-[600px]"
            >
              <TableHeader className="bg-muted/40">
                <TableRow className="hover:bg-transparent">
                  <TableHead className="pl-4">
                    {t(($) => $.linux_user_pages.tables.item)}
                  </TableHead>
                  <TableHead>
                    {t(($) => $.linux_user_pages.columns.status)}
                  </TableHead>
                  <TableHead>
                    {t(($) => $.linux_user_pages.tables.observed)}
                  </TableHead>
                  <TableHead className="pr-4 text-right">
                    {t(($) => $.linux_user_pages.runtime_versions.actions)}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell className="py-3 pl-4 font-medium">
                    {t(($) => $.linux_user.account)}
                  </TableCell>
                  <TableCell>
                    <LinuxUserStatus
                      state={d.account_state}
                      label={stateLabel(d.account_state)}
                    />
                  </TableCell>
                  <TableCell>
                    <LinuxUserTime value={d.checked_at} />
                  </TableCell>
                  <TableCell className="pr-4 text-right">—</TableCell>
                </TableRow>
                <TableRow>
                  <TableCell className="py-3 pl-4 font-medium">
                    {t(($) => $.linux_user.binding)}
                  </TableCell>
                  <TableCell>
                    <LinuxUserStatus
                      state={d.archived_at ? "archived" : d.state}
                      label={
                        d.archived_at
                          ? t(($) => $.linux_user.archived)
                          : stateLabel(d.state)
                      }
                    />
                  </TableCell>
                  <TableCell>
                    <LinuxUserTime value={d.checked_at} />
                  </TableCell>
                  <TableCell className="pr-4 text-right">—</TableCell>
                </TableRow>
                <TableRow>
                  <TableCell className="py-3 pl-4 font-medium">
                    {t(($) => $.linux_user.daemon)}
                  </TableCell>
                  <TableCell>
                    <LinuxUserStatus
                      state={d.daemon_state}
                      label={stateLabel(d.daemon_state)}
                    />
                  </TableCell>
                  <TableCell>
                    <LinuxUserTime value={d.daemon_checked_at} />
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    {!admin &&
                    !d.archived_at &&
                    onConfigure &&
                    d.verified &&
                    d.workspace_id &&
                    d.state !== "removed" ? (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={busy}
                        onClick={() => onConfigure("upgrade")}
                      >
                        {t(($) => $.computers.actions.upgrade)}
                      </Button>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                </TableRow>
                <TableRow>
                  <TableCell className="py-3 pl-4 font-medium">
                    {t(($) => $.linux_user_pages.latest_operation)}
                  </TableCell>
                  <TableCell className="whitespace-normal">
                    {data.operations.data?.[0] ? (
                      <div className="flex flex-wrap items-center gap-2">
                        <span>
                          {kinds[
                            data.operations.data[0].kind as keyof typeof kinds
                          ] ?? t(($) => $.linux_user.unknown)}
                        </span>
                        <LinuxUserStatus
                          state={data.operations.data[0].state}
                          label={stateLabel(data.operations.data[0].state)}
                        />
                      </div>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell>
                    <LinuxUserTime
                      value={data.operations.data?.[0]?.finished_at}
                    />
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    {operationsHref && data.operations.data?.[0] ? (
                      <AppLink
                        href={operationsHref}
                        className={buttonVariants({
                          variant: "outline",
                          size: "sm",
                        })}
                      >
                        {t(($) => $.linux_user_pages.tables.details_named, {
                          operation:
                            kinds[
                              data.operations.data[0].kind as keyof typeof kinds
                            ] ?? t(($) => $.linux_user.operations),
                        })}
                      </AppLink>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
          <section
            className="space-y-3"
            aria-label={t(($) => $.linux_user_pages.danger)}
          >
            {!admin && !d.archived_at && (
              <div className="space-y-2 border-t pt-3">
                <h3 className="text-body font-semibold">
                  {t(($) => $.linux_user_pages.danger)}
                </h3>
                <div className="flex flex-wrap gap-2">
                  {d.state !== "removed" && (
                    <Button
                      variant="outline"
                      disabled={busy}
                      onClick={() => setAction("remove")}
                    >
                      {t(($) => $.computers.actions.remove)}
                    </Button>
                  )}
                  {d.state === "removed" && (
                    <>
                      <Button
                        variant="outline"
                        disabled={busy}
                        onClick={() => setAction("archive")}
                      >
                        {t(($) => $.linux_user.archive)}
                      </Button>
                      {d.account_state !== "missing" && (
                        <Button
                          variant="destructive"
                          disabled={busy}
                          onClick={() => setAction("delete_user")}
                        >
                          {t(($) => $.linux_user.delete_user)}
                        </Button>
                      )}
                    </>
                  )}
                </div>
                {d.state !== "removed" && (
                  <p className="text-sm text-muted-foreground">
                    {t(($) => $.computers.remove_help)}
                  </p>
                )}
                {d.state === "removed" && (
                  <p className="text-sm text-muted-foreground">
                    {t(($) => $.linux_user.archive_help)}{" "}
                    {d.account_state !== "missing" &&
                      t(($) => $.linux_user.delete_help)}
                  </p>
                )}
              </div>
            )}
          </section>
        </>
      )}
      {d && view === "all" && (
        <>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm break-all">
            <dt>{t(($) => $.computers.title)}</dt>
            <dd>{d.computer_name}</dd>
            <dt>{t(($) => $.computers.username)}</dt>
            <dd>{d.username}</dd>
            <dt>{t(($) => $.admin.linux_user_workspace)}</dt>
            <dd>
              {d.workspace_name ||
                t(($) => $.linux_user_pages.workspace.unknown)}
            </dd>
            <dt>{t(($) => $.linux_user.binding)}</dt>
            <dd>
              {d.archived_at
                ? t(($) => $.linux_user.archived)
                : stateLabel(d.state)}
            </dd>
            <dt>{t(($) => $.linux_user.account)}</dt>
            <dd>{stateLabel(d.account_state)}</dd>
            <dt>{t(($) => $.linux_user.daemon)}</dt>
            <dd>
              {stateLabel(d.daemon_state)} · {d.daemon_id}
            </dd>
            <dt>{t(($) => $.linux_user.daemon_checked)}</dt>
            <dd>
              {d.daemon_checked_at
                ? new Date(d.daemon_checked_at).toLocaleString()
                : "—"}
            </dd>
            <dt>{t(($) => $.linux_user.last_check)}</dt>
            <dd>
              {d.checked_at ? new Date(d.checked_at).toLocaleString() : "—"}
            </dd>
            <dt>{t(($) => $.linux_user.last_seen)}</dt>
            <dd>
              {d.last_seen_at ? new Date(d.last_seen_at).toLocaleString() : "—"}
            </dd>
          </dl>
          {["failed", "interrupted", "detached", "removed"].includes(
            d.state,
          ) && <p>{t(($) => $.linux_user.recovery_help)}</p>}
          <Button
            variant="outline"
            disabled={data.check.isPending}
            onClick={() => data.check.mutate()}
          >
            {t(($) => $.admin.linux_user_check)}
          </Button>
          {!admin && !d.archived_at && (
            <div className="flex flex-wrap gap-2">
              {onConfigure &&
                (["removed", "detached", "failed"].includes(d.state) ||
                  !d.verified) && (
                  <Button
                    variant="outline"
                    onClick={() => onConfigure("create_account")}
                    disabled={busy}
                  >
                    {t(($) => $.computers.actions.create_account)}
                  </Button>
                )}
              {onConfigure &&
                d.verified &&
                d.workspace_id &&
                d.state !== "removed" && (
                  <>
                    <Button
                      variant="outline"
                      onClick={() => onConfigure("upgrade")}
                      disabled={busy}
                    >
                      {t(($) => $.computers.actions.upgrade)}
                    </Button>
                  </>
                )}
              {["ready", "failed", "pending"].includes(d.state) && (
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => data.discover.mutate()}
                >
                  {t(($) => $.linux_user.discover)}
                </Button>
              )}
              {d.state !== "removed" && (
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => setAction("remove")}
                >
                  {t(($) => $.computers.actions.remove)}
                </Button>
              )}
              {d.state === "removed" && (
                <>
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() => setAction("archive")}
                  >
                    {t(($) => $.linux_user.archive)}
                  </Button>
                  {d.account_state !== "missing" && (
                    <Button
                      variant="destructive"
                      disabled={busy}
                      onClick={() => setAction("delete_user")}
                    >
                      {t(($) => $.linux_user.delete_user)}
                    </Button>
                  )}
                </>
              )}
            </div>
          )}
        </>
      )}
      {d && view !== "operations" && (
        <Dialog
          open={!!action}
          onOpenChange={(open) => {
            if (!open && !data.lifecycle.isPending) {
              setAction(null);
              setPassword("");
              setConfirmation("");
              data.lifecycle.reset();
            }
          }}
        >
          {action && (
            <DialogContent>
              <DialogHeader>
                <DialogTitle>
                  {action === "delete_user"
                    ? t(($) => $.linux_user.delete_user)
                    : action === "archive"
                      ? t(($) => $.linux_user.archive)
                      : t(($) => $.computers.actions.remove)}
                </DialogTitle>
              </DialogHeader>
              <form
                className="space-y-2 rounded-lg border p-3"
                onSubmit={async (event) => {
                  event.preventDefault();
                  try {
                    await data.lifecycle.mutateAsync({
                      action,
                      confirm_username: confirmation,
                      ...(action !== "archive" ? { password } : {}),
                    });
                    setAction(null);
                    setConfirmation("");
                    setPassword("");
                    data.lifecycle.reset();
                  } catch {
                    /* The mutation error stays visible. */
                  }
                }}
              >
                {data.lifecycle.error && (
                  <p role="alert" className="text-destructive">
                    {data.lifecycle.error.message}
                  </p>
                )}
                <p>
                  {action === "delete_user"
                    ? t(($) => $.linux_user.delete_help)
                    : action === "archive"
                      ? t(($) => $.linux_user.archive_help)
                      : t(($) => $.computers.remove_help)}
                </p>
                <label className="block">
                  {t(($) => $.linux_user.confirm_username, {
                    username: d.username,
                  })}
                  <Input
                    required
                    value={confirmation}
                    onChange={(e) => setConfirmation(e.target.value)}
                  />
                </label>
                {action !== "archive" && (
                  <LinuxPasswordInput
                    key={`${bindingId}:${action}`}
                    label={t(($) => $.linux_user.password_for, {
                      username: d.username,
                    })}
                    required
                    autoComplete="off"
                    value={password}
                    disabled={busy}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                )}
                <div className="flex gap-2">
                  <Button
                    type="submit"
                    variant="destructive"
                    disabled={busy || confirmation !== d.username}
                  >
                    {t(($) => $.linux_user.confirm)}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={data.lifecycle.isPending}
                    onClick={() => {
                      setAction(null);
                      setPassword("");
                      setConfirmation("");
                      data.lifecycle.reset();
                    }}
                  >
                    {t(($) => $.linux_user.cancel)}
                  </Button>
                </div>
              </form>
            </DialogContent>
          )}
        </Dialog>
      )}
      {view !== "overview" &&
        data.operations.isPending &&
        !data.detail.isPending && (
          <p role="status">{t(($) => $.computers.loading)}</p>
        )}
      {view !== "overview" &&
        !data.operations.isPending &&
        !data.operations.error && (
          <LinuxUserOperationsTable
            operations={data.operations.data ?? []}
            operationId={operationId}
            recovering={data.recover.isPending}
            onRecover={
              admin
                ? undefined
                : (op) =>
                    data.recover.mutate({
                      id: op.id,
                      action: op.state === "queued" ? "cancel" : "acknowledge",
                    })
            }
          />
        )}
      {children?.(busy)}
    </section>
  );
}
