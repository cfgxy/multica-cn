"use client";

import {
  bindingSummary,
  bindingWorkspaceLabel,
  type ComputerBinding,
} from "@multica/core/computers";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { ChevronRight } from "lucide-react";
import { AppLink } from "../navigation";
import { useT } from "../i18n";
import { LinuxUserStatus } from "./linux-user-table-parts";

export function LinuxUsersTable({
  accounts,
  machineName,
  href,
  showWorkspace = true,
}: {
  accounts: ComputerBinding[];
  machineName: (id: string) => string;
  href: (binding: ComputerBinding, operations?: boolean) => string;
  showWorkspace?: boolean;
}) {
  const { t } = useT("settings");
  const states = t(($) => $.linux_user_pages.states, { returnObjects: true });
  const operationStates = t(($) => $.linux_user.states, {
    returnObjects: true,
  });
  const kinds = t(($) => $.linux_user.kinds, { returnObjects: true });
  const hints = t(($) => $.linux_user_pages.hints, { returnObjects: true });
  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <Table
        aria-label={t(($) => $.computers.personal_title)}
        className={showWorkspace ? "min-w-[800px]" : "min-w-[640px]"}
      >
        <TableHeader className="bg-muted/40">
          <TableRow className="hover:bg-transparent">
            <TableHead className="sticky left-0 z-20 bg-muted pl-4">
              {t(($) => $.linux_user_pages.columns.user)}
            </TableHead>
            <TableHead>
              {t(($) => $.linux_user_pages.columns.computer)}
            </TableHead>
            {showWorkspace && (
              <TableHead>
                {t(($) => $.linux_user_pages.columns.workspace)}
              </TableHead>
            )}
            <TableHead>{t(($) => $.linux_user_pages.columns.status)}</TableHead>
            <TableHead>
              {t(($) => $.linux_user_pages.latest_operation)}
            </TableHead>
            <TableHead className="pr-4 text-right">
              {t(($) => $.linux_user_pages.runtime_versions.actions)}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.map((binding) => {
            const status = bindingSummary(binding);
            const workspace = bindingWorkspaceLabel(binding);
            const op = binding.latest_operation;
            return (
              <TableRow key={binding.id}>
                <TableCell className="sticky left-0 z-10 max-w-48 bg-card py-3 pl-4 font-medium whitespace-normal break-words">
                  {binding.username}
                </TableCell>
                <TableCell className="max-w-48 whitespace-normal break-words">
                  {machineName(binding.computer_id)}
                </TableCell>
                {showWorkspace && (
                  <TableCell className="max-w-48 whitespace-normal break-words">
                    {workspace.name ??
                      t(($) => $.linux_user_pages.workspace[workspace.key!])}
                  </TableCell>
                )}
                <TableCell className="max-w-64 whitespace-normal">
                  <LinuxUserStatus
                    state={status}
                    label={
                      states[status as keyof typeof states] ??
                      t(($) => $.linux_user.unknown)
                    }
                  />
                  {status !== "ready" && (
                    <p className="mt-1 text-caption text-muted-foreground">
                      {hints[status as keyof typeof hints] ?? hints.unknown}
                    </p>
                  )}
                </TableCell>
                <TableCell className="max-w-52 whitespace-normal">
                  {op ? (
                    <AppLink
                      href={href(binding, true)}
                      className="inline-flex flex-col gap-1 rounded-sm text-caption underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-ring"
                    >
                      <span>
                        {kinds[op.kind as keyof typeof kinds] ??
                          t(($) => $.linux_user.operations)}
                      </span>
                      <span
                        className={
                          op.state === "failed" || op.state === "interrupted"
                            ? "text-destructive"
                            : "text-muted-foreground"
                        }
                      >
                        {operationStates[
                          op.state as keyof typeof operationStates
                        ] ?? t(($) => $.linux_user.unknown)}
                      </span>
                    </AppLink>
                  ) : (
                    "—"
                  )}
                </TableCell>
                <TableCell className="pr-4 text-right">
                  <AppLink
                    href={href(binding)}
                    className={buttonVariants({
                      variant: "outline",
                      size: "sm",
                    })}
                  >
                    {t(($) => $.linux_user.details)}
                    <ChevronRight aria-hidden="true" />
                  </AppLink>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
