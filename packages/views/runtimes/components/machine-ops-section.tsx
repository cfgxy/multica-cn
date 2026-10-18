"use client";

// RUYI-645: node operations section on the runtime detail page. Rendered
// only for workspace owner/admin on a local machine with a daemon (the
// server enforces the same gate; this keeps the surface invisible to
// everyone else). Command results are the node's raw output — the panel
// always labels them as possibly containing sensitive information (Review
// ruling 3).
import { useMemo, useState } from "react";
import { Loader2, ShieldAlert, TerminalSquare } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  opsInFlight,
  runtimeOpsCommandsOptions,
  runtimeOpsInventoryOptions,
  useSubmitRuntimeOpsCommand,
} from "@multica/core/runtimes/ops";
import type {
  RuntimeOpsCommand,
  RuntimeOpsService,
} from "@multica/core/types";

const OPS_IN_FLIGHT_POLL_MS = 3_000;
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
import type { RuntimeMachine } from "./runtime-machines";
import { useT, useTimeAgo } from "../../i18n";

const OPS_KIND_NAMES = [
  "systemctl_status",
  "journalctl_tail",
  "ps_snapshot",
  "df_report",
  "launchctl_list",
  "mac_log_show",
  "docker_ps",
] as const;

type OpsKind = (typeof OPS_KIND_NAMES)[number];

interface OpsKindSpec {
  kind: OpsKind;
  unit: boolean;
  lines?: boolean;
  minutes?: boolean;
}

// Mirror of the server's seven-kind whitelist (server/pkg/opskind). The
// server remains authoritative — an unknown or unauthorized kind is rejected
// there — this only shapes the launcher UI.
const OPS_KINDS: OpsKindSpec[] = [
  { kind: "systemctl_status", unit: true },
  { kind: "journalctl_tail", unit: true, lines: true },
  { kind: "ps_snapshot", unit: false },
  { kind: "df_report", unit: false },
  { kind: "launchctl_list", unit: false },
  { kind: "mac_log_show", unit: false, minutes: true },
  { kind: "docker_ps", unit: false },
];

const LINES_DEFAULT = 100;
const TIMEOUT_DEFAULT = 30;

// Same pick order as machineUpdateRuntime: an online runtime wins so the
// daemon receives the command on its next heartbeat immediately.
export function machineOpsRuntime(machine: RuntimeMachine) {
  if (machine.mode !== "local") return null;
  return (
    machine.runtimes.find((runtime) => runtime.status === "online") ??
    machine.runtimes[0] ??
    null
  );
}

function OpsStatusBadge({ status }: { status: RuntimeOpsCommand["status"] }) {
  const { t } = useT("runtimes");
  const tone =
    status === "succeeded"
      ? "text-emerald-600 dark:text-emerald-400"
      : status === "pending" || status === "claimed"
        ? "text-amber-600 dark:text-amber-400"
        : "text-destructive";
  return (
    <span className={`inline-flex items-center gap-1 text-caption font-medium ${tone}`}>
      {status === "pending" || status === "claimed" ? (
        <Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />
      ) : null}
      {t(($) => $.machine.ops.status[status])}
    </span>
  );
}

function ServiceRow({ service }: { service: RuntimeOpsService }) {
  const tone =
    service.status === "running" || service.status === "active"
      ? "text-emerald-600 dark:text-emerald-400"
      : service.status === "failed"
        ? "text-destructive"
        : "text-muted-foreground";
  return (
    <div className="flex items-baseline justify-between gap-3 border-b py-1.5 last:border-b-0">
      <div className="min-w-0">
        <span className="block truncate font-mono text-caption text-foreground">
          {service.name}
        </span>
        {service.detail && (
          <span className="block truncate text-micro text-muted-foreground">
            {service.detail}
          </span>
        )}
      </div>
      <span className={`shrink-0 text-caption ${tone}`}>{service.status}</span>
    </div>
  );
}

function CommandRow({ command }: { command: RuntimeOpsCommand }) {
  const { t } = useT("runtimes");
  const timeAgo = useTimeAgo();
  const [open, setOpen] = useState(false);
  const hasResult =
    !!command.result &&
    (!!command.result.stdout || !!command.result.stderr || !!command.result.truncated);
  const terminal =
    command.status === "succeeded" ||
    command.status === "failed" ||
    command.status === "rejected";

  return (
    <div className="border-b py-2 last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="font-mono text-caption text-foreground">
          {t(($) => $.machine.ops.kinds[command.kind as OpsKind]) ?? command.kind}
        </span>
        <OpsStatusBadge status={command.status} />
        {command.exit_code !== null && command.exit_code !== undefined && (
          <span className="text-micro text-muted-foreground">
            {t(($) => $.machine.ops.exit_code, { code: command.exit_code })}
          </span>
        )}
        <span className="text-micro text-muted-foreground">
          {timeAgo(command.created_at)}
        </span>
        {command.status === "rejected" && command.reject_reason && (
          <span className="text-micro text-destructive">
            {command.reject_reason}
          </span>
        )}
        {hasResult && (
          <button
            type="button"
            className="text-micro text-muted-foreground underline-offset-2 hover:underline"
            onClick={() => setOpen((prev) => !prev)}
          >
            {open ? t(($) => $.machine.ops.hide_output) : t(($) => $.machine.ops.show_output)}
          </button>
        )}
      </div>
      {open && hasResult && (
        <div className="mt-2">
          <p className="mb-1 flex items-center gap-1 text-micro text-amber-600 dark:text-amber-400">
            <ShieldAlert aria-hidden="true" className="h-3 w-3" />
            {t(($) => $.machine.ops.sensitive_notice)}
          </p>
          <pre className="max-h-72 overflow-auto rounded-md bg-muted p-2 font-mono text-micro whitespace-pre-wrap">
            {command.result?.stdout}
            {command.result?.stderr ? `\n[stderr]\n${command.result.stderr}` : ""}
            {command.result?.truncated ? `\n[${t(($) => $.machine.ops.truncated)}]` : ""}
          </pre>
        </div>
      )}
      {!terminal && command.status !== "pending" && command.status !== "claimed" && (
        <p className="mt-1 text-micro text-muted-foreground">
          {t(($) => $.machine.ops.terminal_note)}
        </p>
      )}
    </div>
  );
}

export function MachineOpsSection({ machine }: { machine: RuntimeMachine }) {
  const { t } = useT("runtimes");
  const timeAgo = useTimeAgo();
  const target = useMemo(() => machineOpsRuntime(machine), [machine]);

  const inventory = useQuery(runtimeOpsInventoryOptions(target?.id ?? ""));
  const commands = useQuery({
    ...runtimeOpsCommandsOptions(target?.id ?? ""),
    refetchInterval: (query) =>
      opsInFlight(query.state.data as RuntimeOpsCommand[] | undefined)
        ? OPS_IN_FLIGHT_POLL_MS
        : false,
  });
  const submit = useSubmitRuntimeOpsCommand(target?.id ?? "");

  const [kind, setKind] = useState<OpsKind>("systemctl_status");
  const [unit, setUnit] = useState<string>("");
  const [scalar, setScalar] = useState<number>(LINES_DEFAULT);
  const spec = OPS_KINDS.find((entry) => entry.kind === kind);

  const services = inventory.data?.services ?? [];
  const online = target?.status === "online";
  const canSubmit = !!online && !!target && !submit.isPending;

  const onSubmit = () => {
    if (!target || !spec) return;
    const args: Record<string, unknown> = {};
    if (spec.unit && unit) args.unit = unit;
    if (spec.lines) args.lines = scalar;
    if (spec.minutes) args.minutes = scalar;
    submit.mutate({ kind, args, timeout_s: TIMEOUT_DEFAULT });
  };

  // Nothing to offer without a daemon-owned runtime (e.g. Desktop's
  // synthesized stopped-daemon placeholder row).
  if (machine.runtimes.length === 0 || !target) return null;

  return (
    <section className="rounded-lg border bg-card" aria-label={t(($) => $.machine.ops.title)}>
      <div className="border-b px-4 py-3">
        <h3 className="flex items-center gap-2 text-body font-semibold">
          <TerminalSquare aria-hidden="true" className="h-4 w-4 text-muted-foreground" />
          {t(($) => $.machine.ops.title)}
        </h3>
        <p className="mt-0.5 text-caption text-muted-foreground">
          {t(($) => $.machine.ops.subtitle)}
        </p>
      </div>

      <div className="grid gap-4 p-4 lg:grid-cols-2">
        <div>
          <h4 className="text-caption font-medium text-muted-foreground">
            {t(($) => $.machine.ops.inventory_title)}
            {inventory.data?.last_success_at && (
              <span className="ml-2 font-normal">
                {t(($) => $.machine.ops.inventory_as, { time: timeAgo(inventory.data.last_success_at) })}
              </span>
            )}
          </h4>
          <div className="mt-2 max-h-56 overflow-y-auto rounded-md border p-2">
            {inventory.isLoading ? (
              <div className="flex items-center gap-2 p-2 text-caption text-muted-foreground">
                <Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />
                {t(($) => $.machine.ops.loading)}
              </div>
            ) : inventory.data?.collect_status === "ok" ? (
              services.length > 0 ? (
                services.map((service) => (
                  <ServiceRow key={service.name} service={service} />
                ))
              ) : (
                <p className="p-2 text-caption text-muted-foreground">
                  {t(($) => $.machine.ops.inventory_empty)}
                </p>
              )
            ) : (
              <p className="p-2 text-caption text-destructive">
                {inventory.data?.error || t(($) => $.machine.ops.inventory_unavailable)}
              </p>
            )}
          </div>
        </div>

        <div>
          <h4 className="text-caption font-medium text-muted-foreground">
            {t(($) => $.machine.ops.run_title)}
          </h4>
          {!online && (
            <p className="mt-2 text-caption text-muted-foreground">
              {t(($) => $.machine.ops.offline_hint)}
            </p>
          )}
          <div className="mt-2 space-y-2">
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <Label className="text-caption text-muted-foreground">
                  {t(($) => $.machine.ops.kind_label)}
                </Label>
                <Select
                  items={OPS_KINDS.map((entry) => ({
                    value: entry.kind,
                    label: t(($) => $.machine.ops.kinds[entry.kind]),
                  }))}
                  value={kind}
                  onValueChange={(next) => {
                    setKind(next ?? kind);
                    setScalar(LINES_DEFAULT);
                  }}
                >
                  <SelectTrigger className="h-9 w-full rounded-md text-body">
                    <SelectValue>
                      {() => <span className="truncate">{t(($) => $.machine.ops.kinds[kind])}</span>}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent align="start">
                    {OPS_KINDS.map((entry) => (
                      <SelectItem key={entry.kind} value={entry.kind}>
                        {t(($) => $.machine.ops.kinds[entry.kind])}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              {spec?.unit && (
                <div className="space-y-1">
                  <Label className="text-caption text-muted-foreground">
                    {t(($) => $.machine.ops.unit_label)}
                  </Label>
                  <Select
                    items={services.map((service) => ({
                      value: service.name,
                      label: service.name,
                    }))}
                    value={unit}
                    onValueChange={(next) => setUnit(next ?? unit)}
                  >
                    <SelectTrigger className="h-9 w-full rounded-md text-body">
                      <SelectValue>
                        {() => (
                          <span className="truncate">
                            {unit || t(($) => $.machine.ops.unit_placeholder)}
                          </span>
                        )}
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent align="start">
                      {services.map((service) => (
                        <SelectItem key={service.name} value={service.name}>
                          {service.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              )}
              {(spec?.lines || spec?.minutes) && (
                <div className="space-y-1">
                  <Label className="text-caption text-muted-foreground">
                    {spec.lines
                      ? t(($) => $.machine.ops.lines_label)
                      : t(($) => $.machine.ops.minutes_label)}
                  </Label>
                  <Input
                    type="number"
                    min={spec.lines ? 50 : 10}
                    max={spec.lines ? 500 : 120}
                    value={scalar}
                    onChange={(event) => {
                      const next = Number(event.target.value);
                      if (Number.isFinite(next)) setScalar(next);
                    }}
                    className="h-9"
                  />
                </div>
              )}
            </div>
            <Button type="button" size="sm" disabled={!canSubmit || (spec?.unit === true && !unit)} onClick={onSubmit}>
              {submit.isPending && <Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />}
              {t(($) => $.machine.ops.run_button)}
            </Button>
            {submit.isError && (
              <p className="text-caption text-destructive">
                {t(($) => $.machine.ops.submit_failed)}
              </p>
            )}
          </div>
        </div>
      </div>

      <div className="border-t px-4 py-3">
        <h4 className="text-caption font-medium text-muted-foreground">
          {t(($) => $.machine.ops.history_title)}
        </h4>
        <div className="mt-1 max-h-72 overflow-y-auto">
          {(commands.data ?? []).length > 0 ? (
            commands.data!.map((command) => (
              <CommandRow key={command.id} command={command} />
            ))
          ) : (
            <p className="py-2 text-caption text-muted-foreground">
              {t(($) => $.machine.ops.history_empty)}
            </p>
          )}
        </div>
      </div>
    </section>
  );
}
