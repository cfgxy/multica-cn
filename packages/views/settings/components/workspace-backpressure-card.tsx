"use client";

import { useEffect, useState } from "react";
import { Input } from "@multica/ui/components/ui/input";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";
import {
  useSaveWorkspaceBackpressureSettings,
  workspaceBackpressureSettingsOptions,
} from "@multica/core/workspace";
import type { WorkspaceBackpressureSettingsSave } from "@multica/core/types";
import { useT } from "../../i18n";
import { SettingsCard, SettingsRow } from "./settings-layout";

/**
 * Host-backpressure card (RUYI-618). The workspace's watermarks for pausing
 * new task claims on daemon hosts, saved per workspace and delivered to
 * running daemons on the next heartbeat ack. Saves are explicit (nine
 * coupled fields with cross-field hysteresis rules — auto-save would fire
 * on every intermediate, invalid state), and the server's 422 message is
 * the shared validation sentence the daemon itself enforces.
 */

type FieldName =
  | "mem_high_pct"
  | "mem_recovery_pct"
  | "swap_high_pct"
  | "swap_recovery_pct"
  | "psi_high_pct"
  | "psi_recovery_pct"
  | "sample_interval_seconds"
  | "window_size";

const NUMERIC_FIELDS: FieldName[] = [
  "mem_high_pct",
  "mem_recovery_pct",
  "swap_high_pct",
  "swap_recovery_pct",
  "psi_high_pct",
  "psi_recovery_pct",
  "sample_interval_seconds",
  "window_size",
];

type Draft = Record<FieldName, string>;

function toDraft(values: {
  mem_high_pct: number;
  mem_recovery_pct: number;
  swap_high_pct: number;
  swap_recovery_pct: number;
  psi_high_pct: number;
  psi_recovery_pct: number;
  sample_interval_seconds: number;
  window_size: number;
}): Draft {
  return {
    mem_high_pct: String(values.mem_high_pct),
    mem_recovery_pct: String(values.mem_recovery_pct),
    swap_high_pct: String(values.swap_high_pct),
    swap_recovery_pct: String(values.swap_recovery_pct),
    psi_high_pct: String(values.psi_high_pct),
    psi_recovery_pct: String(values.psi_recovery_pct),
    sample_interval_seconds: String(values.sample_interval_seconds),
    window_size: String(values.window_size),
  };
}

/** Unparsable or empty fields block the save; the server re-validates. */
function parseDraft(draft: Draft): WorkspaceBackpressureSettingsSave | null {
  const nums: Partial<Record<FieldName, number>> = {};
  for (const field of NUMERIC_FIELDS) {
    const raw = draft[field].trim();
    if (raw === "") return null;
    const n = Number(raw);
    if (!Number.isFinite(n)) return null;
    nums[field] = n;
  }
  return {
    enabled: true,
    mem_high_pct: nums.mem_high_pct!,
    mem_recovery_pct: nums.mem_recovery_pct!,
    swap_high_pct: nums.swap_high_pct!,
    swap_recovery_pct: nums.swap_recovery_pct!,
    psi_high_pct: nums.psi_high_pct!,
    psi_recovery_pct: nums.psi_recovery_pct!,
    sample_interval_seconds: nums.sample_interval_seconds!,
    window_size: nums.window_size!,
  };
}

export function WorkspaceBackpressureCard({
  wsId,
  canManage,
}: {
  wsId: string;
  /** Owners save; everyone else reads the effective card. */
  canManage: boolean;
}) {
  const { t } = useT("settings");
  const settings = useQuery(workspaceBackpressureSettingsOptions(wsId));
  const save = useSaveWorkspaceBackpressureSettings(wsId);

  const [enabled, setEnabled] = useState(true);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [custom, setCustom] = useState(false);
  const [invalidReason, setInvalidReason] = useState<string | null>(null);

  useEffect(() => {
    const data = settings.data;
    if (!data || loaded) return;
    setEnabled(data.enabled);
    setDraft(toDraft(data));
    setCustom(data.custom);
    setLoaded(true);
  }, [settings.data, loaded]);

  if (settings.isPending || !draft) {
    return (
      <SettingsCard>
        <div className="flex flex-col gap-3">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      </SettingsCard>
    );
  }

  const parsed = parseDraft(draft);
  const saveDisabled = !canManage || save.isPending || parsed === null;

  const handleSave = async () => {
    if (!parsed) return;
    setInvalidReason(null);
    try {
      const saved = await save.mutateAsync({ ...parsed, enabled });
      setEnabled(saved.enabled);
      setDraft(toDraft(saved));
      setCustom(true);
      toast.success(t(($) => $.workspace.bp_toast_saved));
    } catch (error) {
      // A 422 body is { ok: false, message } — the shared validation
      // sentence. Anything else (network, 500) gets the generic toast.
      const message =
        error instanceof Error && error.message ? error.message : null;
      if (message) {
        setInvalidReason(message);
      } else {
        toast.error(t(($) => $.workspace.bp_toast_failed));
      }
    }
  };

  const numericInput = (
    field: FieldName,
    aria: string,
    opts: { step?: string; min?: string } = {},
  ) => (
    <Input
      type="number"
      inputMode="decimal"
      step={opts.step ?? "0.5"}
      min={opts.min}
      className="w-24"
      disabled={!canManage}
      aria-label={aria}
      value={draft[field]}
      onChange={(e) => {
        const next = { ...draft, [field]: e.target.value };
        setDraft(next);
        setInvalidReason(null);
      }}
    />
  );

  return (
    <SettingsCard>
      <SettingsRow
        label={t(($) => $.workspace.bp_enabled)}
        description={t(($) => $.workspace.bp_enabled_hint)}
      >
        <Switch checked={enabled} onCheckedChange={setEnabled} disabled={!canManage} />
      </SettingsRow>

      <SettingsRow
        label={t(($) => $.workspace.bp_mem_label)}
        description={t(($) => $.workspace.bp_mem_hint)}
      >
        <div className="flex items-center gap-2">
          {numericInput("mem_high_pct", t(($) => $.workspace.bp_high_aria), { min: "0" })}
          {numericInput("mem_recovery_pct", t(($) => $.workspace.bp_recovery_aria), { min: "0" })}
        </div>
      </SettingsRow>

      <SettingsRow
        label={t(($) => $.workspace.bp_swap_label)}
        description={t(($) => $.workspace.bp_swap_hint)}
      >
        <div className="flex items-center gap-2">
          {numericInput("swap_high_pct", t(($) => $.workspace.bp_high_aria), { min: "0" })}
          {numericInput("swap_recovery_pct", t(($) => $.workspace.bp_recovery_aria), { min: "0" })}
        </div>
      </SettingsRow>

      <SettingsRow
        label={t(($) => $.workspace.bp_psi_label)}
        description={t(($) => $.workspace.bp_psi_hint)}
      >
        <div className="flex items-center gap-2">
          {numericInput("psi_high_pct", t(($) => $.workspace.bp_high_aria), { min: "0" })}
          {numericInput("psi_recovery_pct", t(($) => $.workspace.bp_recovery_aria), { min: "0" })}
        </div>
      </SettingsRow>

      <SettingsRow
        label={t(($) => $.workspace.bp_sampling_label)}
        description={t(($) => $.workspace.bp_sampling_hint)}
      >
        <div className="flex items-center gap-2">
          {numericInput("sample_interval_seconds", t(($) => $.workspace.bp_interval_aria), { step: "1", min: "1" })}
          {numericInput("window_size", t(($) => $.workspace.bp_window_aria), { step: "1", min: "1" })}
        </div>
      </SettingsRow>

      {!custom && canManage && (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.workspace.bp_defaults_note)}
        </p>
      )}

      {invalidReason && (
        <p role="alert" className="text-body text-destructive">
          {t(($) => $.workspace.bp_invalid)} {invalidReason}
        </p>
      )}

      {canManage && (
        <div className="flex items-center gap-2">
          <Button size="sm" disabled={saveDisabled} onClick={() => void handleSave()}>
            {save.isPending ? t(($) => $.workspace.saving) : t(($) => $.workspace.save)}
          </Button>
        </div>
      )}
    </SettingsCard>
  );
}
