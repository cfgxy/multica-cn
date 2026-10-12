"use client";

import { useEffect, useState, type ReactNode } from "react";
import { ArrowDown, ArrowUp, Info } from "lucide-react";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@multica/ui/components/ui/input-group";
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@multica/ui/components/ui/field";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
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
import {
  SettingsCard,
  SettingsRow,
  SettingsSaveState,
  type SettingsSaveStatus,
} from "./settings-layout";

/**
 * Host-backpressure card (RUYI-618, reworked in RUYI-740). The workspace's
 * watermarks for pausing new task claims on daemon hosts, saved per
 * workspace and delivered to running daemons on the next heartbeat ack.
 * Saves are explicit (nine coupled fields with cross-field hysteresis rules
 * — auto-save would fire on every intermediate, invalid state). The
 * client-side validator mirrors the server's `Validate` so owners see
 * violations while typing; the server's 422 message stays as the fallback
 * for rule drift.
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

type FieldLabelKey =
  | "bp_pause_label"
  | "bp_resume_label"
  | "bp_interval_label"
  | "bp_window_label";

type FieldUnitKey = "bp_unit_pct" | "bp_unit_seconds" | "bp_unit_samples";

/** The workspace.* i18n keys a validation failure can render. */
type DraftErrorKey =
  | "bp_err_mem_pause_positive"
  | "bp_err_range"
  | "bp_err_mem_order"
  | "bp_err_down_order"
  | "bp_err_range_1_3600";

/** Protocol ceiling for both sampling fields (MaxBackpressureSampleIntervalSeconds / MaxBackpressureWindowSize). */
const MAX_SAMPLING_VALUE = 3600;

/** Small column geometry shared by every numeric cell so the four rows line up. */
const FIELD_COL = "flex min-w-0 flex-1 flex-col sm:w-28 sm:flex-none";
const ARROW_COL = "w-6 shrink-0";

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

/**
 * Field-level mirror of protocol.DaemonBackpressureConfig.Validate
 * (server/pkg/protocol/backpressure.go): same predicates, one entry per
 * offending field, values are workspace.* i18n keys. Empty or unparsable
 * fields produce no entry — incompleteness is parseDraft's signal, and
 * mid-typing states must not flash red. A relation violation is charged to
 * the recovery side only (the value breaking its own rule) so a row never
 * goes double-red. Swap/PSI only constrain their recovery value when the
 * pause threshold is > 0: 0 disables the row, while on memory 0 is a
 * hard error (it would pause every claim forever).
 */
export function validateBackpressureDraft(
  draft: Draft,
): Partial<Record<FieldName, DraftErrorKey>> {
  const errors: Partial<Record<FieldName, DraftErrorKey>> = {};
  const num = (field: FieldName): number | null => {
    const raw = draft[field].trim();
    if (raw === "") return null;
    const n = Number(raw);
    return Number.isFinite(n) ? n : null;
  };

  const memHigh = num("mem_high_pct");
  if (memHigh !== null) {
    if (memHigh <= 0) errors.mem_high_pct = "bp_err_mem_pause_positive";
    else if (memHigh > 100) errors.mem_high_pct = "bp_err_range";
  }
  const memRecovery = num("mem_recovery_pct");
  if (memRecovery !== null) {
    if (memRecovery > 100) errors.mem_recovery_pct = "bp_err_range";
    else if (memHigh !== null && memRecovery <= memHigh) {
      errors.mem_recovery_pct = "bp_err_mem_order";
    }
  }

  for (const [highField, recoveryField] of [
    ["swap_high_pct", "swap_recovery_pct"],
    ["psi_high_pct", "psi_recovery_pct"],
  ] as const) {
    const high = num(highField);
    if (high !== null && high > 100) errors[highField] = "bp_err_range";
    if (high !== null && high > 0) {
      const recovery = num(recoveryField);
      if (recovery !== null && (recovery < 0 || recovery >= high)) {
        errors[recoveryField] = "bp_err_down_order";
      }
    }
  }

  for (const field of ["sample_interval_seconds", "window_size"] as const) {
    const n = num(field);
    if (n !== null && (!Number.isInteger(n) || n < 1 || n > MAX_SAMPLING_VALUE)) {
      errors[field] = "bp_err_range_1_3600";
    }
  }
  return errors;
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
  const fieldErrors = validateBackpressureDraft(draft);
  const invalidCount = Object.keys(fieldErrors).length;
  const incomplete = parsed === null;
  const saveDisabled =
    !canManage || save.isPending || incomplete || invalidCount > 0;
  const saveStatus: SettingsSaveStatus = save.isPending
    ? "saving"
    : incomplete || invalidCount > 0
      ? "error"
      : "idle";
  const clientErrorLabel = incomplete
    ? t(($) => $.workspace.bp_err_incomplete)
    : t(($) => $.workspace.bp_invalid_count, { count: invalidCount });

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

  const setField = (field: FieldName, value: string) => {
    setDraft({ ...draft, [field]: value });
    setInvalidReason(null);
  };

  const errorText = (key: DraftErrorKey) => t(($) => $.workspace[key]);

  /**
   * One labeled numeric cell. The label is visible (htmlFor-linked, replacing
   * the old aria-only keys) and the unit lives inside the box as an addon;
   * `aria-invalid` on the input lights the InputGroup's red border.
   */
  const numericField = (
    id: string,
    field: FieldName,
    labelKey: FieldLabelKey,
    unitKey: FieldUnitKey,
    opts: { step?: string; min?: string; max?: string } = {},
  ) => (
    <Field
      className={`${FIELD_COL} gap-1`}
      data-invalid={fieldErrors[field] ? true : undefined}
    >
      <FieldLabel
        htmlFor={id}
        className="text-caption font-normal leading-4 text-muted-foreground"
      >
        {t(($) => $.workspace[labelKey])}
      </FieldLabel>
      <InputGroup>
        <InputGroupInput
          id={id}
          type="number"
          inputMode="decimal"
          step={opts.step ?? "0.5"}
          min={opts.min}
          max={opts.max}
          disabled={!canManage}
          aria-invalid={fieldErrors[field] ? true : undefined}
          value={draft[field]}
          onChange={(e) => setField(field, e.target.value)}
        />
        <InputGroupAddon align="inline-end">
          <InputGroupText>{t(($) => $.workspace[unitKey])}</InputGroupText>
        </InputGroupAddon>
      </InputGroup>
    </Field>
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
        <ThresholdPair
          direction="up"
          high={numericField("bp-mem-high", "mem_high_pct", "bp_pause_label", "bp_unit_pct", { min: "0" })}
          highError={
            fieldErrors.mem_high_pct ? errorText(fieldErrors.mem_high_pct) : null
          }
          recovery={numericField("bp-mem-recovery", "mem_recovery_pct", "bp_resume_label", "bp_unit_pct", { min: "0" })}
          recoveryError={
            fieldErrors.mem_recovery_pct
              ? errorText(fieldErrors.mem_recovery_pct)
              : null
          }
        />
      </SettingsRow>

      <SettingsRow
        label={t(($) => $.workspace.bp_swap_label)}
        description={t(($) => $.workspace.bp_swap_hint)}
      >
        <ThresholdPair
          direction="down"
          sentinel={t(($) => $.workspace.bp_sentinel_note)}
          high={numericField("bp-swap-high", "swap_high_pct", "bp_pause_label", "bp_unit_pct", { min: "0" })}
          highError={
            fieldErrors.swap_high_pct ? errorText(fieldErrors.swap_high_pct) : null
          }
          recovery={numericField("bp-swap-recovery", "swap_recovery_pct", "bp_resume_label", "bp_unit_pct", { min: "0" })}
          recoveryError={
            fieldErrors.swap_recovery_pct
              ? errorText(fieldErrors.swap_recovery_pct)
              : null
          }
        />
      </SettingsRow>

      <SettingsRow
        label={
          <span className="inline-flex items-center gap-1">
            {t(($) => $.workspace.bp_psi_label)}
            <TooltipProvider delay={0}>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <button
                      type="button"
                      aria-label={t(($) => $.workspace.bp_psi_info)}
                      className="cursor-help rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    >
                      <Info
                        className="size-3.5 text-muted-foreground"
                        aria-hidden="true"
                      />
                    </button>
                  }
                />
                <TooltipContent
                  side="top"
                  className="max-w-80 whitespace-normal break-all"
                >
                  {t(($) => $.workspace.bp_psi_info)}
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </span>
        }
        description={t(($) => $.workspace.bp_psi_hint)}
      >
        <ThresholdPair
          direction="down"
          sentinel={t(($) => $.workspace.bp_sentinel_note)}
          high={numericField("bp-psi-high", "psi_high_pct", "bp_pause_label", "bp_unit_pct", { min: "0" })}
          highError={
            fieldErrors.psi_high_pct ? errorText(fieldErrors.psi_high_pct) : null
          }
          recovery={numericField("bp-psi-recovery", "psi_recovery_pct", "bp_resume_label", "bp_unit_pct", { min: "0" })}
          recoveryError={
            fieldErrors.psi_recovery_pct
              ? errorText(fieldErrors.psi_recovery_pct)
              : null
          }
        />
      </SettingsRow>

      <SettingsRow
        label={t(($) => $.workspace.bp_sampling_label)}
        description={t(($) => $.workspace.bp_sampling_hint)}
      >
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex items-start gap-2">
            {numericField("bp-interval", "sample_interval_seconds", "bp_interval_label", "bp_unit_seconds", {
              step: "1",
              min: "1",
              max: "3600",
            })}
            <div className={`${ARROW_COL} shrink-0`} aria-hidden="true" />
            {numericField("bp-window", "window_size", "bp_window_label", "bp_unit_samples", {
              step: "1",
              min: "1",
              max: "3600",
            })}
          </div>
          {(fieldErrors.sample_interval_seconds || fieldErrors.window_size) && (
            <div className="flex items-start gap-2">
              <div className={FIELD_COL}>
                {fieldErrors.sample_interval_seconds && (
                  <FieldError className="text-caption">
                    {errorText(fieldErrors.sample_interval_seconds)}
                  </FieldError>
                )}
              </div>
              <div className={`${ARROW_COL} shrink-0`} aria-hidden="true" />
              <div className={FIELD_COL}>
                {fieldErrors.window_size && (
                  <FieldError className="text-caption">
                    {errorText(fieldErrors.window_size)}
                  </FieldError>
                )}
              </div>
            </div>
          )}
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
          <SettingsSaveState
            status={saveStatus}
            savingLabel={t(($) => $.workspace.saving)}
            savedLabel=""
            errorLabel={clientErrorLabel}
          />
        </div>
      )}
    </SettingsCard>
  );
}

/**
 * The two threshold boxes of one watermark row plus the direction arrow
 * between them. The arrow's title makes the per-row hysteresis direction
 * (memory recovery must rise above; swap/PSI recovery must sit below)
 * readable without parsing the hint. Errors and the 0-sentinel render below
 * the boxes in their own columns, so the boxes never shift when they appear.
 */
function ThresholdPair({
  high,
  highError,
  recovery,
  recoveryError,
  sentinel,
  direction,
}: {
  high: ReactNode;
  highError: string | null;
  recovery: ReactNode;
  recoveryError: string | null;
  /** Persistent "0 disables this check" note under the pause box (swap/PSI only). */
  sentinel?: string;
  direction: "up" | "down";
}) {
  const { t } = useT("settings");
  const ArrowIcon = direction === "up" ? ArrowUp : ArrowDown;
  const dirText =
    direction === "up"
      ? t(($) => $.workspace.bp_dir_up_title)
      : t(($) => $.workspace.bp_dir_down_title);
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <div className="flex items-start gap-2">
        {high}
        <div
          className={`flex justify-center self-end pb-2 ${ARROW_COL}`}
          title={dirText}
          aria-label={dirText}
        >
          <ArrowIcon className="size-4 text-muted-foreground" aria-hidden="true" />
        </div>
        {recovery}
      </div>
      {(sentinel || highError || recoveryError) && (
        <div className="flex items-start gap-2">
          <div className={FIELD_COL}>
            {highError ? (
              <FieldError className="text-caption">{highError}</FieldError>
            ) : sentinel ? (
              <FieldDescription className="text-caption">
                {sentinel}
              </FieldDescription>
            ) : null}
          </div>
          <div className={`${ARROW_COL} shrink-0`} aria-hidden="true" />
          <div className={FIELD_COL}>
            {recoveryError && (
              <FieldError className="text-caption">{recoveryError}</FieldError>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
