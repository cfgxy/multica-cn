"use client";

import { useState, useEffect } from "react";
import {
  useComputers,
  type ComputerBinding,
  type ComputerOperation,
} from "@multica/core/computers";
import { useWorkspaceList } from "@multica/core/workspace";
import { useCurrentWorkspace } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { clientErrorMessage, errorCode } from "@multica/core/api";
import { LinuxPasswordInput } from "./linux-password-input";
import { useT } from "../i18n";

/** The caller owns the dialog; unmounting it discards the password. */
export function LinuxUserOperationForm({
  userId,
  binding,
  action = "create_account",
  workspaceId,
  onAccepted,
  onBusyChange,
}: {
  userId: string;
  binding?: ComputerBinding;
  action?: "create_account" | "upgrade";
  workspaceId?: string;
  onAccepted: (binding: ComputerBinding) => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const { t } = useT("settings");
  const data = useComputers(userId);
  const current = useCurrentWorkspace();
  const { workspaces, ready, unavailable } = useWorkspaceList();
  const [machine, setMachine] = useState(binding?.computer_id ?? "");
  const [username, setUsername] = useState(binding?.username ?? "");
  const [workspace, setWorkspace] = useState(
    workspaceId || binding?.workspace_id || current?.id || "",
  );
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const machines = data.machines.data?.filter((m) => m.enabled) ?? [];
  const fixedWorkspace =
    workspaceId || (action === "upgrade" ? binding?.workspace_id : undefined);
  const targetWorkspace = fixedWorkspace || workspace;
  const busy = data.operate.isPending;
  useEffect(() => {
    onBusyChange?.(busy);
  }, [busy, onBusyChange]);
  useEffect(() => () => onBusyChange?.(false), [onBusyChange]);
  const allowed =
    machines.some((m) => m.id === machine) &&
    workspaces.some((w) => w.id === targetWorkspace);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!allowed || busy || !password) return;
    setError("");
    try {
      const result = await data.operate.mutateAsync({
        computer_id: machine,
        workspace_id: targetWorkspace,
        username: username.trim(),
        password,
        action,
      } satisfies ComputerOperation);
      setPassword("");
      onAccepted(result);
    } catch (err) {
      const code = errorCode(err);
      const messages = t(($) => $.workspace_linux_users.errors, {
        returnObjects: true,
      });
      setError(
        code && Object.hasOwn(messages, code)
          ? messages[code as keyof typeof messages]
          : (clientErrorMessage(err) ?? t(($) => $.computers.failed)),
      );
    } finally {
      setPassword("");
      data.operate.reset();
    }
  }
  return (
    <form onSubmit={(event) => void submit(event)} className="space-y-4">
      {binding ? (
        <p className="break-words">
          {machines.find((m) => m.id === machine)?.name ??
            t(($) => $.linux_user.unknown)}{" "}
          · {username}
        </p>
      ) : (
        <>
          <label className="block space-y-1">
            <span>{t(($) => $.computers.choose)}</span>
            <Select
              value={machine}
              onValueChange={(value) => setMachine(value ?? "")}
              items={machines.map((m) => ({ value: m.id, label: m.name }))}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder={t(($) => $.computers.choose)} />
              </SelectTrigger>
              <SelectContent>
                {machines.map((m) => (
                  <SelectItem key={m.id} value={m.id}>
                    {m.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
          <label className="block space-y-1">
            <span>{t(($) => $.computers.username)}</span>
            <Input
              required
              value={username}
              pattern="[a-z_][a-z0-9_-]{0,31}"
              onChange={(e) => setUsername(e.target.value)}
            />
          </label>
        </>
      )}
      {fixedWorkspace ? (
        <p>
          {t(($) => $.admin.linux_user_workspace)}:{" "}
          {workspaces.find((w) => w.id === fixedWorkspace)?.name ??
            t(($) => $.linux_user_pages.workspace.unknown)}
        </p>
      ) : (
        <label className="block space-y-1">
          <span>{t(($) => $.admin.linux_user_workspace)}</span>
          <Select
            value={workspace}
            onValueChange={(value) => setWorkspace(value ?? "")}
            items={workspaces.map((w) => ({ value: w.id, label: w.name }))}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {workspaces.map((w) => (
                <SelectItem key={w.id} value={w.id}>
                  {w.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </label>
      )}
      {action === "create_account" && (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.workspace_linux_users.account_help)}
        </p>
      )}
      {action === "create_account" && (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.workspace_linux_users.new_account_help)}
        </p>
      )}
      <LinuxPasswordInput
        autoComplete="off"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      {!data.machines.isPending && machines.length === 0 && (
        <p>{t(($) => $.workspace_linux_users.no_computers)}</p>
      )}
      {(unavailable || (ready && !workspaces.length)) && (
        <p role="alert">{t(($) => $.linux_user_pages.no_workspace)}</p>
      )}
      {(error || data.machines.error) && (
        <p role="alert" className="text-destructive">
          {error || data.machines.error?.message}
        </p>
      )}
      <Button
        type="submit"
        disabled={busy || !allowed || !username.trim() || !password}
        aria-busy={busy}
      >
        {t(($) => $.computers.actions[action])}
      </Button>
    </form>
  );
}
