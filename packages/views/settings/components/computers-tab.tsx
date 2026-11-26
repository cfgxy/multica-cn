"use client";

import { useState } from "react";
import { useComputers, type ComputerBinding } from "@multica/core/computers";
import { useAuthStore } from "@multica/core/auth";
import { Plus } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useNavigation } from "../../navigation";
import { SettingsTab } from "./settings-layout";
import { LinuxUserOperationForm } from "../../computers/linux-user-operation-form";
import { LinuxUsersTable } from "../../computers/linux-users-table";
import { LinuxUserPage } from "../../computers/linux-user-page";
import { linuxUserHref, resolveLinuxUserLocation } from "./settings-navigation";
import { useT } from "../../i18n";

export function ComputersTab() {
  const userId = useAuthStore((s) => s.user?.id ?? "");
  const navigation = useNavigation();
  const location = resolveLinuxUserLocation(navigation.searchParams);
  if (location.id)
    return (
      <LinuxUserPage
        key={`${userId}:${location.id}`}
        userId={userId}
        bindingId={location.id}
      />
    );
  return <PersonalComputersList key={userId} userId={userId} />;
}

function PersonalComputersList({ userId }: { userId: string }) {
  const { t } = useT("settings");
  const data = useComputers(userId);
  const navigation = useNavigation();
  const location = resolveLinuxUserLocation(navigation.searchParams);
  const [open, setOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const machineName = (id: string) =>
    data.machines.data?.find((m) => m.id === id)?.name ??
    t(($) => $.linux_user.unknown);
  const href = (binding: ComputerBinding, operations = false) =>
    linuxUserHref(navigation.pathname, navigation.searchParams, {
      id: binding.id,
      view: operations ? "operations" : "overview",
      operation: operations ? binding.latest_operation?.id : undefined,
    });
  const accounts =
    data.bindings.data?.filter((b) =>
      `${b.username} ${machineName(b.computer_id)}`
        .toLocaleLowerCase()
        .includes(location.search.toLocaleLowerCase()),
    ) ?? [];
  return (
    <SettingsTab
      title={
        <span className="flex flex-wrap items-center justify-between gap-3">
          <span>{t(($) => $.computers.personal_title)}</span>
          <Dialog
            open={open}
            onOpenChange={(value) => {
              if (!submitting) setOpen(value);
            }}
          >
            <DialogTrigger render={<Button />}>
              <Plus aria-hidden="true" />
              {t(($) => $.linux_user_pages.create)}
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>{t(($) => $.linux_user_pages.create)}</DialogTitle>
              </DialogHeader>
              {open && (
                <LinuxUserOperationForm
                  onBusyChange={setSubmitting}
                  userId={userId}
                  onAccepted={(b) => {
                    setOpen(false);
                    navigation.push(href(b, true));
                  }}
                />
              )}
            </DialogContent>
          </Dialog>
        </span>
      }
    >
      {((data.bindings.data?.length ?? 0) > 0 || location.search) && (
        <Input
          aria-label={t(($) => $.linux_user_pages.search)}
          placeholder={t(($) => $.linux_user_pages.search)}
          value={location.search}
          onChange={(e) =>
            navigation.replace(
              linuxUserHref(navigation.pathname, navigation.searchParams, {
                search: e.target.value,
              }),
            )
          }
        />
      )}
      {(data.bindings.error || data.machines.error) && (
        <p role="alert" className="text-destructive">
          {data.bindings.error?.message || data.machines.error?.message}
        </p>
      )}
      {data.bindings.isPending && (
        <p role="status">{t(($) => $.computers.loading)}</p>
      )}
      {!data.bindings.isPending && accounts.length === 0 && (
        <p>
          {location.search
            ? t(($) => $.linux_user_pages.empty_search)
            : t(($) => $.linux_user_pages.empty_accounts)}
        </p>
      )}
      {!data.bindings.isPending &&
        !location.search &&
        !data.bindings.error &&
        !data.machines.error &&
        data.bindings.data !== undefined &&
        data.machines.data !== undefined &&
        (data.bindings.data?.length ?? 0) === 0 &&
        (data.machines.data?.filter((m) => m.enabled).length ?? 0) === 0 &&
        !data.machines.isPending && (
          <p>{t(($) => $.workspace_linux_users.no_computers)}</p>
        )}
      {accounts.length > 0 && (
        <LinuxUsersTable
          accounts={accounts}
          machineName={machineName}
          href={href}
        />
      )}
    </SettingsTab>
  );
}
