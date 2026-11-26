"use client";

import { useState } from "react";
import {
  bindingPrimaryAction,
  useLinuxUserDetail,
} from "@multica/core/computers";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from "@multica/ui/components/ui/alert-dialog";
import {
  ArrowLeft,
  History,
  KeyRound,
  LayoutDashboard,
  Terminal,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { AppLink, useNavigation } from "../navigation";
import { useT } from "../i18n";
import { SettingsTab } from "../settings/components/settings-layout";
import {
  linuxUserHref,
  linuxUserViews,
  resolveLinuxUserLocation,
  settingsHref,
} from "../settings/components/settings-navigation";
import { LinuxUserDetail } from "./linux-user-detail";
import { BindingRuntimes } from "./binding-runtimes";
import { LinuxUserOperationForm } from "./linux-user-operation-form";
import { AccountCredentials } from "../settings/components/credentials-tab";

const viewIcons = {
  overview: LayoutDashboard,
  credentials: KeyRound,
  runtimes: Terminal,
  operations: History,
};

export function LinuxUserPage({
  userId,
  bindingId,
}: {
  userId: string;
  bindingId: string;
}) {
  const { t } = useT("settings");
  const navigation = useNavigation();
  const location = resolveLinuxUserLocation(navigation.searchParams);
  const data = useLinuxUserDetail(userId, bindingId);
  const d = data.detail.data;
  const needsRecovery =
    data.operations.data?.some(
      (operation) =>
        operation.state === "interrupted" && !operation.finished_at,
    ) ?? false;
  const [action, setAction] = useState<"create_account" | "upgrade" | null>(
    null,
  );
  const [dirty, setDirty] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [leaving, setLeaving] = useState<string | null>(null);
  const href = (view: typeof location.view) =>
    linuxUserHref(navigation.pathname, navigation.searchParams, {
      id: bindingId,
      view,
      fromWorkspace: location.fromWorkspace,
    });
  const primary = d ? bindingPrimaryAction(d) : null;
  const back = location.fromWorkspace
    ? settingsHref(navigation.pathname, navigation.searchParams, "linux-users")
    : linuxUserHref(navigation.pathname, navigation.searchParams);
  const guard = (event: React.MouseEvent, target: string) => {
    if (
      dirty &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.shiftKey &&
      event.button === 0
    ) {
      event.preventDefault();
      setLeaving(target);
    }
  };
  return (
    <SettingsTab
      title={
        <span className="flex flex-wrap items-center justify-between gap-3">
          <span>
            {d
              ? `${d.username} · ${d.computer_name}`
              : t(($) => $.linux_user.details)}
          </span>
          {primary === "create_account" && (
            <Button
              disabled={data.busy || needsRecovery}
              onClick={() => setAction("create_account")}
            >
              {t(($) => $.computers.actions.create_account)}
            </Button>
          )}
          {primary === "credentials" && (
            <AppLink
              href={href("credentials")}
              onClick={(event) => guard(event, href("credentials"))}
              className={buttonVariants({ variant: "default", size: "sm" })}
            >
              {t(($) => $.linux_user_pages.primary_credentials)}
            </AppLink>
          )}
          {primary === "operations" && (
            <AppLink
              href={href("operations")}
              onClick={(event) => guard(event, href("operations"))}
              className={buttonVariants({ variant: "default", size: "sm" })}
            >
              {t(($) => $.linux_user_pages.primary_operations)}
            </AppLink>
          )}
        </span>
      }
    >
      <AppLink
        href={back}
        onClick={(e) => guard(e, back)}
        className={buttonVariants({ variant: "ghost", size: "sm" })}
      >
        <ArrowLeft aria-hidden="true" />
        {location.fromWorkspace
          ? t(($) => $.linux_user_pages.back_workspace)
          : t(($) => $.linux_user_pages.back)}
      </AppLink>
      <nav
        aria-label={t(($) => $.linux_user.details)}
        className="grid grid-cols-2 gap-1 rounded-lg border bg-muted/50 p-1 sm:grid-cols-4"
      >
        {linuxUserViews.map((view) => {
          const Icon = viewIcons[view];
          return (
            <AppLink
              key={view}
              href={href(view)}
              onClick={(e) => guard(e, href(view))}
              aria-current={view === location.view ? "page" : undefined}
              className={cn(
                "inline-flex min-h-10 flex-1 items-center justify-center gap-2 rounded-md border px-3 py-2 text-body font-medium whitespace-nowrap transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
                view === location.view
                  ? "border-border bg-background text-foreground shadow-sm hover:bg-background"
                  : "border-transparent text-muted-foreground hover:bg-background/60 hover:text-foreground",
              )}
            >
              <Icon className="size-4" aria-hidden="true" />
              {t(($) => $.linux_user_pages.views[view])}
            </AppLink>
          );
        })}
      </nav>
      {data.detail.error && (
        <p role="alert" className="text-destructive">
          {data.detail.error.message}
        </p>
      )}
      {data.detail.isPending && (
        <p role="status">{t(($) => $.computers.loading)}</p>
      )}
      {(data.busy || needsRecovery) &&
        primary !== "operations" &&
        location.view !== "operations" && (
          <AppLink
            className={buttonVariants({ variant: "secondary", size: "sm" })}
            href={href("operations")}
            onClick={(event) => guard(event, href("operations"))}
          >
            {needsRecovery
              ? t(($) => $.linux_user_pages.states.interrupted)
              : t(($) => $.linux_user_pages.states.running)}{" "}
            · {t(($) => $.linux_user.operations)}
          </AppLink>
        )}
      {d && (
        <div key={location.view}>
          {(location.view === "overview" || location.view === "operations") && (
            <LinuxUserDetail
              userId={userId}
              bindingId={bindingId}
              view={location.view}
              operationId={location.operation}
              operationsHref={href("operations")}
              onConfigure={setAction}
            />
          )}
          {location.view === "runtimes" &&
            (["ready", "pending", "failed"].includes(d.state) &&
            !d.archived_at &&
            d.verified &&
            d.account_state !== "missing" ? (
              <BindingRuntimes
                accountLabel={`${d.username} · ${d.computer_name}`}
                userId={userId}
                bindingId={bindingId}
                busy={
                  needsRecovery ||
                  data.busy ||
                  d.operation_busy ||
                  d.state === "running" ||
                  d.state === "interrupted"
                }
              />
            ) : (
              <p>{t(($) => $.linux_user_pages.configure_first)}</p>
            ))}
          {location.view === "credentials" && !d.archived_at && (
            <AccountCredentials
              userId={userId}
              binding={{
                ...d,
                operation_busy: d.operation_busy || data.busy || needsRecovery,
              }}
              onDirtyChange={setDirty}
            />
          )}
        </div>
      )}
      <Dialog
        open={!!action}
        onOpenChange={(open) => {
          if (!open && !submitting) setAction(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {action ? t(($) => $.computers.actions[action]) : ""}
            </DialogTitle>
          </DialogHeader>
          {action && d && (
            <LinuxUserOperationForm
              onBusyChange={setSubmitting}
              userId={userId}
              binding={d}
              action={action}
              onAccepted={() => {
                setAction(null);
                navigation.push(href("operations"));
              }}
            />
          )}
        </DialogContent>
      </Dialog>
      <AlertDialog
        open={!!leaving}
        onOpenChange={(open) => {
          if (!open) setLeaving(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.linux_user_pages.discard_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.linux_user_pages.discard_help)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel render={<Button variant="outline" />}>
              {t(($) => $.linux_user.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              render={<Button />}
              onClick={() => {
                if (leaving) {
                  setDirty(false);
                  navigation.push(leaving);
                  setLeaving(null);
                }
              }}
            >
              {t(($) => $.linux_user.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsTab>
  );
}
