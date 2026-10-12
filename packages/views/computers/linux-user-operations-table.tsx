"use client";

import { Fragment, useState } from "react";
import type { RemoteOperation } from "@multica/core/computers";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { Button } from "@multica/ui/components/ui/button";
import { ChevronDown, Copy } from "lucide-react";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import { useT } from "../i18n";
import { LinuxUserStatus, LinuxUserTime } from "./linux-user-table-parts";

type Props = {
  operations: RemoteOperation[];
  operationId?: string | null;
  recovering: boolean;
  onRecover?: (operation: RemoteOperation) => void;
};

export function LinuxUserOperationsTable({ operations, ...props }: Props) {
  const { t } = useT("settings");
  return (
    <section
      className="space-y-3"
      aria-label={t(($) => $.linux_user.operations)}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-body font-semibold">
          {t(($) => $.linux_user.operations)}
        </h3>
        <span className="text-caption text-muted-foreground">
          {t(($) => $.linux_user_pages.tables.recent_limit)}
        </span>
      </div>
      {operations.length === 0 ? (
        <p>{t(($) => $.linux_user.no_operations)}</p>
      ) : (
        <div className="overflow-hidden rounded-lg border bg-card">
          <Table
            aria-label={t(($) => $.linux_user.operations)}
            className="min-w-[880px]"
          >
            <TableHeader className="bg-muted/40">
              <TableRow className="hover:bg-transparent">
                <TableHead className="sticky left-0 z-20 bg-muted pl-4">
                  {t(($) => $.linux_user_pages.tables.operation)}
                </TableHead>
                <TableHead>
                  {t(($) => $.linux_user_pages.columns.status)}
                </TableHead>
                <TableHead>
                  {t(($) => $.linux_user_pages.tables.step)}
                </TableHead>
                <TableHead>
                  {t(($) => $.linux_user_pages.tables.versions)}
                </TableHead>
                <TableHead>
                  {t(($) => $.linux_user_pages.tables.created)}
                </TableHead>
                <TableHead>
                  {t(($) => $.linux_user_pages.tables.duration)}
                </TableHead>
                <TableHead className="pr-4 text-right">
                  {t(($) => $.linux_user_pages.runtime_versions.actions)}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {operations.map((operation) => (
                <OperationRow
                  key={operation.id}
                  operation={operation}
                  {...props}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  );
}

function OperationRow({
  operation: op,
  operationId,
  recovering,
  onRecover,
}: Omit<Props, "operations"> & { operation: RemoteOperation }) {
  const { t } = useT("settings");
  const [expanded, setExpanded] = useState(false);
  const states = t(($) => $.linux_user.states, { returnObjects: true });
  const kinds = t(($) => $.linux_user.kinds, { returnObjects: true });
  const steps = t(($) => $.linux_user.steps, { returnObjects: true });
  const errors = t(($) => $.linux_user.errors, { returnObjects: true });
  const kind =
    kinds[op.kind as keyof typeof kinds] ?? t(($) => $.linux_user.unknown);
  const failure = op.error_code
    ? (errors[op.error_code as keyof typeof errors] ?? op.error_summary)
    : op.error_summary;
  const copyError = async () => {
    const text = JSON.stringify(
      {
        operation_id: op.id,
        binding_id: op.binding_id,
        kind: op.kind,
        runtime: op.runtime_id,
        state: op.state,
        step: op.step,
        requested_version: op.requested_version,
        actual_version: op.actual_version,
        error_code: op.error_code,
        error: failure || t(($) => $.linux_user.unknown),
        created_at: op.created_at,
        started_at: op.started_at,
        finished_at: op.finished_at,
      },
      null,
      2,
    );
    if (await copyText(text)) {
      toast.success(t(($) => $.linux_user_pages.tables.error_copied));
    } else {
      toast.error(t(($) => $.linux_user_pages.tables.error_copy_failed));
    }
  };
  const duration =
    op.started_at && op.finished_at
      ? Math.max(
          0,
          Math.round(
            (new Date(op.finished_at).getTime() -
              new Date(op.started_at).getTime()) /
              1000,
          ),
        )
      : null;
  const selected = operationId === op.id;
  const recoverable =
    op.state === "queued" || (op.state === "interrupted" && !op.finished_at);
  const detailsId = `operation-details-${op.id}`;
  return (
    <Fragment>
      <TableRow
        id={`operation-${op.id}`}
        aria-current={selected ? "true" : undefined}
        className={selected ? "bg-accent hover:bg-accent" : undefined}
      >
        <TableCell
          className={`sticky left-0 z-10 max-w-48 py-3 pl-4 whitespace-normal ${selected ? "bg-accent" : "bg-card"}`}
        >
          <span className="font-medium">{kind}</span>
          {op.runtime_id && (
            <span className="mt-1 block font-mono text-caption text-muted-foreground">
              {op.runtime_id}
            </span>
          )}
        </TableCell>
        <TableCell>
          <LinuxUserStatus
            state={op.state}
            label={
              states[op.state as keyof typeof states] ??
              t(($) => $.linux_user.unknown)
            }
          />
        </TableCell>
        <TableCell className="max-w-64 whitespace-normal">
          <span>
            {steps[op.step as keyof typeof steps] ??
              t(($) => $.linux_user.unknown)}
          </span>
          {failure && !expanded && (
            <p className="mt-1 line-clamp-2 text-caption text-destructive">
              {failure}
            </p>
          )}
        </TableCell>
        <TableCell className="max-w-48 whitespace-normal break-words font-mono text-caption tabular-nums">
          {op.requested_version ? (
            <span>
              {op.requested_version} → {op.actual_version || "—"}
            </span>
          ) : (
            "—"
          )}
        </TableCell>
        <TableCell>
          <LinuxUserTime value={op.created_at} />
        </TableCell>
        <TableCell className="text-caption tabular-nums">
          {duration !== null && Number.isFinite(duration)
            ? t(($) => $.linux_user_pages.tables.seconds, { count: duration })
            : "—"}
        </TableCell>
        <TableCell className="pr-4">
          <div className="flex items-center justify-end gap-2">
            {op.state === "failed" && (
              <Button variant="outline" size="sm" onClick={copyError}>
                <Copy aria-hidden="true" />
                {t(($) => $.linux_user_pages.tables.copy_error)}
              </Button>
            )}
            {onRecover && recoverable && (
              <Button
                variant="outline"
                size="sm"
                disabled={recovering}
                aria-busy={recovering}
                onClick={() => onRecover(op)}
              >
                {op.state === "queued"
                  ? t(($) => $.linux_user.cancel)
                  : t(($) => $.linux_user.acknowledge)}
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon-sm"
              aria-expanded={expanded}
              aria-controls={detailsId}
              aria-label={t(($) => $.linux_user_pages.tables.details_named, {
                operation: `${kind}${op.runtime_id ? ` ${op.runtime_id}` : ""}`,
              })}
              onClick={() => setExpanded(!expanded)}
            >
              <ChevronDown
                aria-hidden="true"
                className={expanded ? "rotate-180" : undefined}
              />
            </Button>
          </div>
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow id={detailsId} className="bg-muted/20 hover:bg-muted/20">
          <TableCell colSpan={7} className="whitespace-normal px-4 py-3">
            <div className="max-w-[min(48rem,calc(100vw-5rem))]">
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-caption [&_dt]:text-muted-foreground">
                <dt>{t(($) => $.linux_user_pages.tables.started)}</dt>
                <dd>
                  <LinuxUserTime value={op.started_at} />
                </dd>
                <dt>{t(($) => $.linux_user_pages.tables.finished)}</dt>
                <dd>
                  <LinuxUserTime value={op.finished_at} />
                </dd>
              </dl>
              {failure && (
                <p className="mt-3 max-w-3xl whitespace-pre-wrap break-words text-caption text-destructive">
                  {failure}
                </p>
              )}
            </div>
          </TableCell>
        </TableRow>
      )}
    </Fragment>
  );
}
