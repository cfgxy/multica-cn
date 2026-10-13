"use client";

// Daemon host backpressure settings card (RUYI-729 stage 1) — the Runtime
// detail page's configuration entry for the gate that pauses new task
// claims when the host runs low on memory/swap or stalls on PSI.
//
// Read model: the effective card (saved row, or code defaults with a
// "defaults" marker) plus the daemon's live report rendered as
// current-value-vs-threshold per condition. Write model: owner/admin only —
// the same bar as the scheduling-freeze controls, because a bad threshold
// here can stall every task on the host. Saves land in the DB, ride the
// next heartbeat ack to the daemon, and rebuild its state machine without a
// restart. The trigger/recovery timeline at the bottom answers "why did
// everything stall at noon" with the config snapshot in force per episode.

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { useT, useTimeAgo } from "../../i18n";
import type { AgentRuntime } from "@multica/core/types";
import type {
  RuntimeBackpressureSettings,
  RuntimeBackpressureEvent,
} from "@multica/core/api/schemas";
import {
  runtimeBackpressureSettingsOptions,
  runtimeBackpressureEventsOptions,
} from "@multica/core/runtimes/queries";
import {
  useSaveRuntimeBackpressureSettings,
  useResetRuntimeBackpressureSettings,
} from "@multica/core/runtimes/mutations";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { ShieldAlert, History, Pencil, RotateCcw } from "lucide-react";

type BpConfig = NonNullable<RuntimeBackpressureSettings["config"]>;

/** Form mirror of the wire card — snake_case straight through, so the save
 * body is the state object itself and there is no second mapping to drift.
 * A type alias (not an interface) so it stays assignable to the loose wire
 * type's index signature. */
type BpForm = {
  enabled: boolean;
  mem_high_pct: number;
  mem_recovery_pct: number;
  swap_high_pct: number;
  swap_recovery_pct: number;
  psi_high_pct: number;
  psi_recovery_pct: number;
  sample_interval_seconds: number;
  window_size: number;
}

const DEFAULT_FORM: BpForm = {
  enabled: true,
  mem_high_pct: 15,
  mem_recovery_pct: 25,
  swap_high_pct: 80,
  swap_recovery_pct: 60,
  psi_high_pct: 50,
  psi_recovery_pct: 20,
  sample_interval_seconds: 5,
  window_size: 6,
};

function toForm(cfg: BpConfig): BpForm {
  return {
    enabled: cfg.enabled !== false,
    mem_high_pct: cfg.mem_high_pct,
    mem_recovery_pct: cfg.mem_recovery_pct,
    swap_high_pct: cfg.swap_high_pct,
    swap_recovery_pct: cfg.swap_recovery_pct,
    psi_high_pct: cfg.psi_high_pct,
    psi_recovery_pct: cfg.psi_recovery_pct,
    sample_interval_seconds: cfg.sample_interval_seconds,
    window_size: cfg.window_size,
  };
}

/**
 * Client-side mirror of protocol.ValidateBackpressureConfig — the same
 * hysteresis discipline the server enforces, run before the request so an
 * invalid save never leaves the form. Returns an error-code string the
 * caller maps to a localized message.
 */
function validateForm(f: BpForm): string | null {
  if (!Number.isFinite(f.mem_high_pct) || f.mem_high_pct <= 0 || f.mem_high_pct >= 100)
    return "mem_high";
  if (
    !Number.isFinite(f.mem_recovery_pct) ||
    f.mem_recovery_pct <= f.mem_high_pct ||
    f.mem_recovery_pct > 100
  )
    return "mem_hysteresis";
  if (f.swap_high_pct > 0) {
    if (f.swap_high_pct > 100) return "swap_high";
    if (
      !Number.isFinite(f.swap_recovery_pct) ||
      f.swap_recovery_pct < 0 ||
      f.swap_recovery_pct >= f.swap_high_pct
    )
      return "swap_hysteresis";
  }
  if (f.psi_high_pct > 0) {
    if (f.psi_high_pct > 100) return "psi_high";
    if (
      !Number.isFinite(f.psi_recovery_pct) ||
      f.psi_recovery_pct < 0 ||
      f.psi_recovery_pct >= f.psi_high_pct
    )
      return "psi_hysteresis";
  }
  if (!Number.isFinite(f.sample_interval_seconds) || f.sample_interval_seconds < 1)
    return "interval";
  if (!Number.isFinite(f.window_size) || f.window_size < 1) return "window";
  return null;
}

export function BackpressureSettingsCard({
  runtime,
  canEdit,
}: {
  runtime: AgentRuntime;
  canEdit: boolean;
}) {
  const { t } = useT("runtimes");
  const timeAgo = useTimeAgo();
  const { data: settings } = useQuery(
    runtimeBackpressureSettingsOptions(runtime.id),
  );
  const { data: events = [] } = useQuery(
    runtimeBackpressureEventsOptions(runtime.id),
  );
  const save = useSaveRuntimeBackpressureSettings(runtime.id);
  const reset = useResetRuntimeBackpressureSettings(runtime.id);
  const [editOpen, setEditOpen] = useState(false);
  const [draft, setDraft] = useState<BpForm>(DEFAULT_FORM);

  // Cloud runtimes have no daemon host to gate — the card is a per-machine
  // concept. (The detail page also gates the mount; this keeps the component
  // safe for any future caller.)
  if (runtime.runtime_mode !== "local" || !runtime.daemon_id) return null;

  const condLabel = (condition: string): string => {
    const map: Record<string, string> = {
      mem: t(($) => $.detail.bp_cond_mem),
      swap: t(($) => $.detail.bp_cond_swap),
      psi: t(($) => $.detail.bp_cond_psi),
    };
    return map[condition] ?? condition;
  };

  // Localized text for a validateForm error code (see the comment above it).
  const bpErr = (code: string): string => {
    switch (code) {
      case "mem_high":
        return t(($) => $.detail.bp_err_mem_high);
      case "mem_hysteresis":
        return t(($) => $.detail.bp_err_mem_hysteresis);
      case "swap_high":
        return t(($) => $.detail.bp_err_swap_high);
      case "swap_hysteresis":
        return t(($) => $.detail.bp_err_swap_hysteresis);
      case "psi_high":
        return t(($) => $.detail.bp_err_psi_high);
      case "psi_hysteresis":
        return t(($) => $.detail.bp_err_psi_hysteresis);
      case "interval":
        return t(($) => $.detail.bp_err_interval);
      default:
        return t(($) => $.detail.bp_err_window);
    }
  };

  const effective: BpForm = settings?.config ? toForm(settings.config) : DEFAULT_FORM;
  const liveConditions = settings?.report?.conditions ?? null;

  const openEdit = () => {
    setDraft(effective);
    setEditOpen(true);
  };

  const handleSave = () => {
    const err = validateForm(draft);
    if (err) {
      toast.error(bpErr(err));
      return;
    }
    save.mutate(draft, {
      onSuccess: () => {
        setEditOpen(false);
        toast.success(t(($) => $.detail.bp_save_ok));
      },
      onError: (e) =>
        toast.error(e instanceof Error ? e.message : t(($) => $.detail.bp_save_failed)),
    });
  };

  const handleReset = () => {
    reset.mutate(undefined, {
      onSuccess: () => {
        setEditOpen(false);
        toast.success(t(($) => $.detail.bp_reset_ok));
      },
      onError: (e) =>
        toast.error(e instanceof Error ? e.message : t(($) => $.detail.bp_save_failed)),
    });
  };

  const eventLine = (ev: RuntimeBackpressureEvent): string => {
    if (ev.event_type === "cleared") {
      return t(($) => $.detail.bp_event_deferred, { count: ev.deferred_claims });
    }
    const hits = ev.hits
      .map((h) => `${condLabel(h.condition)} ${h.value}% / ${h.threshold}%`)
      .join(" · ");
    return (
      hits ||
      ev.reason
        .split("+")
        .map((tok) => condLabel(tok.trim()))
        .join(" + ")
    );
  };

  return (
    <section
      data-testid="backpressure-settings-card"
      className="rounded-lg border bg-card p-4"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="flex items-center gap-1.5 text-body font-medium">
            <ShieldAlert className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
            {t(($) => $.detail.bp_card_title)}
          </h3>
          <p className="mt-0.5 text-caption text-muted-foreground">
            {settings?.configured
              ? t(($) => $.detail.bp_source_saved)
              : t(($) => $.detail.bp_source_defaults)}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {settings?.configured && canEdit && (
            <Button
              variant="ghost"
              size="sm"
              onClick={handleReset}
              disabled={reset.isPending}
              data-testid="bp-reset"
            >
              <RotateCcw className="h-3.5 w-3.5" />
              {t(($) => $.detail.bp_reset)}
            </Button>
          )}
          {canEdit && (
            <Button variant="outline" size="sm" onClick={openEdit} data-testid="bp-edit">
              <Pencil className="h-3.5 w-3.5" />
              {t(($) => $.detail.bp_edit)}
            </Button>
          )}
        </div>
      </div>

      <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1.5 text-caption sm:grid-cols-3">
        <Fact label={t(($) => $.detail.bp_state)}>
          {effective.enabled
            ? t(($) => $.detail.bp_state_enabled)
            : t(($) => $.detail.bp_state_disabled)}
        </Fact>
        <Fact label={t(($) => $.detail.bp_mem_band)}>
          {t(($) => $.detail.bp_mem_band_value, {
            high: effective.mem_high_pct,
            recovery: effective.mem_recovery_pct,
          })}
        </Fact>
        <Fact label={t(($) => $.detail.bp_swap_band)}>
          {effective.swap_high_pct > 0
            ? `${effective.swap_high_pct}% → ${effective.swap_recovery_pct}%`
            : t(($) => $.detail.bp_condition_disabled)}
        </Fact>
        <Fact label={t(($) => $.detail.bp_psi_band)}>
          {effective.psi_high_pct > 0
            ? `${effective.psi_high_pct}% → ${effective.psi_recovery_pct}%`
            : t(($) => $.detail.bp_condition_disabled)}
        </Fact>
        <Fact label={t(($) => $.detail.bp_sampling)}>
          {t(($) => $.detail.bp_sampling_value, {
            interval: effective.sample_interval_seconds,
            window: effective.window_size,
          })}
        </Fact>
      </div>

      {liveConditions && liveConditions.length > 0 && (
        <div className="mt-3 rounded-md bg-muted/40 p-2.5" data-testid="bp-live-readings">
          <div className="text-micro font-medium uppercase tracking-wider text-muted-foreground">
            {t(($) => $.detail.bp_live_title)}
          </div>
          <div className="mt-1 grid grid-cols-1 gap-0.5 text-caption sm:grid-cols-3">
            {liveConditions.map((c) => (
              <span key={c.condition} className="flex items-center gap-1">
                <span
                  className={`inline-block h-1.5 w-1.5 rounded-full ${
                    c.triggered ? "bg-warning" : "bg-emerald-500"
                  }`}
                  aria-hidden="true"
                />
                {t(($) => $.detail.bp_cond_reading, {
                  condition: condLabel(c.condition),
                  value: c.value,
                  threshold: c.threshold,
                })}
              </span>
            ))}
          </div>
        </div>
      )}

      {events.length > 0 && (
        <div className="mt-3 border-t pt-3">
          <div className="flex items-center gap-1.5 text-micro font-medium uppercase tracking-wider text-muted-foreground">
            <History className="h-3 w-3" aria-hidden="true" />
            {t(($) => $.detail.bp_events_title)}
          </div>
          <ul className="mt-1.5 space-y-1" data-testid="bp-event-list">
            {events.slice(0, 8).map((ev) => (
              <li key={ev.id} className="flex items-baseline gap-2 text-caption">
                <span
                  className={
                    ev.event_type === "entered"
                      ? "shrink-0 font-medium text-warning"
                      : "shrink-0 font-medium text-emerald-600"
                  }
                >
                  {ev.event_type === "entered"
                    ? t(($) => $.detail.bp_event_entered)
                    : t(($) => $.detail.bp_event_cleared)}
                </span>
                <span className="min-w-0 truncate text-muted-foreground">
                  {eventLine(ev)}
                </span>
                <time
                  dateTime={ev.occurred_at}
                  className="ml-auto shrink-0 text-micro text-muted-foreground"
                >
                  {timeAgo(ev.occurred_at)}
                </time>
              </li>
            ))}
          </ul>
        </div>
      )}

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.detail.bp_edit_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.detail.bp_edit_description)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <Label htmlFor="bp-enabled">{t(($) => $.detail.bp_state)}</Label>
              <Switch
                id="bp-enabled"
                checked={draft.enabled}
                onCheckedChange={(v) => setDraft((d) => ({ ...d, enabled: v === true }))}
              />
            </div>
            <NumField
              id="bp-mem-high"
              label={t(($) => $.detail.bp_mem_high)}
              hint={t(($) => $.detail.bp_mem_high_hint)}
              value={draft.mem_high_pct}
              onChange={(v) => setDraft((d) => ({ ...d, mem_high_pct: v }))}
            />
            <NumField
              id="bp-mem-recovery"
              label={t(($) => $.detail.bp_mem_recovery)}
              value={draft.mem_recovery_pct}
              onChange={(v) => setDraft((d) => ({ ...d, mem_recovery_pct: v }))}
            />
            <NumField
              id="bp-swap-high"
              label={t(($) => $.detail.bp_swap_high)}
              hint={t(($) => $.detail.bp_swap_high_hint)}
              value={draft.swap_high_pct}
              onChange={(v) => setDraft((d) => ({ ...d, swap_high_pct: v }))}
            />
            <NumField
              id="bp-swap-recovery"
              label={t(($) => $.detail.bp_swap_recovery)}
              value={draft.swap_recovery_pct}
              onChange={(v) => setDraft((d) => ({ ...d, swap_recovery_pct: v }))}
            />
            <NumField
              id="bp-psi-high"
              label={t(($) => $.detail.bp_psi_high)}
              hint={t(($) => $.detail.bp_psi_high_hint)}
              value={draft.psi_high_pct}
              onChange={(v) => setDraft((d) => ({ ...d, psi_high_pct: v }))}
            />
            <NumField
              id="bp-psi-recovery"
              label={t(($) => $.detail.bp_psi_recovery)}
              value={draft.psi_recovery_pct}
              onChange={(v) => setDraft((d) => ({ ...d, psi_recovery_pct: v }))}
            />
            <div className="grid grid-cols-2 gap-3">
              <NumField
                id="bp-interval"
                label={t(($) => $.detail.bp_interval)}
                value={draft.sample_interval_seconds}
                onChange={(v) =>
                  setDraft((d) => ({ ...d, sample_interval_seconds: v }))
                }
              />
              <NumField
                id="bp-window"
                label={t(($) => $.detail.bp_window)}
                value={draft.window_size}
                onChange={(v) => setDraft((d) => ({ ...d, window_size: v }))}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setEditOpen(false)}>
              {t(($) => $.detail.bp_cancel)}
            </Button>
            <Button
              size="sm"
              onClick={handleSave}
              disabled={save.isPending}
              data-testid="bp-save"
            >
              {t(($) => $.detail.bp_save)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

// validateForm returns bare codes ("mem_high", "psi_hysteresis", …); each
// maps to its detail-namespace message key, spelled out so the selector API
// stays fully typed against the locale resources.

function Fact({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <span className="flex flex-col">
      <span className="text-micro uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span className="font-mono text-caption text-foreground">{children}</span>
    </span>
  );
}

function NumField({
  id,
  label,
  hint,
  value,
  onChange,
}: {
  id: string;
  label: string;
  hint?: string;
  value: number;
  onChange: (v: number) => void;
}) {
  return (
    <div className="space-y-0.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        step="any"
        value={Number.isFinite(value) ? value : ""}
        onChange={(e) => {
          const n = Number(e.target.value);
          if (Number.isFinite(n)) onChange(n);
        }}
      />
      {hint && <p className="text-micro text-muted-foreground">{hint}</p>}
    </div>
  );
}
