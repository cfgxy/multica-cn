"use client";

import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Gauge, Pencil } from "lucide-react";
import { toast } from "sonner";
import type { AgentRuntime } from "@multica/core/types";
import type { RuntimeModelCallBudgetConfig } from "@multica/core/api/schemas";
import { runtimeModelCallBudgetOptions } from "@multica/core/runtimes/queries";
import { useSaveRuntimeModelCallBudget } from "@multica/core/runtimes/mutations";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { useT, useTimeAgo } from "../../i18n";

const DEFAULT_CONFIG: RuntimeModelCallBudgetConfig = {
  enabled: false,
  max_calls: 1000,
  window_seconds: 86400,
};

const MAX_CALLS = 1_000_000_000;
const MIN_WINDOW_SECONDS = 60;
const MAX_WINDOW_SECONDS = 90 * 24 * 60 * 60;

type BudgetForm = RuntimeModelCallBudgetConfig;

function validateForm(config: BudgetForm): "max_calls" | "window_seconds" | null {
  if (
    !Number.isInteger(config.max_calls) ||
    config.max_calls < 1 ||
    config.max_calls > MAX_CALLS
  ) {
    return "max_calls";
  }
  if (
    !Number.isInteger(config.window_seconds) ||
    config.window_seconds < MIN_WINDOW_SECONDS ||
    config.window_seconds > MAX_WINDOW_SECONDS
  ) {
    return "window_seconds";
  }
  return null;
}

export function ModelCallBudgetCard({
  runtime,
  canEdit,
}: {
  runtime: AgentRuntime;
  canEdit: boolean;
}) {
  const { t } = useT("runtimes");
  const timeAgo = useTimeAgo();
  const { data: budget } = useQuery(runtimeModelCallBudgetOptions(runtime.id));
  const save = useSaveRuntimeModelCallBudget(runtime.id);
  const [editOpen, setEditOpen] = useState(false);
  const [draft, setDraft] = useState<BudgetForm>(DEFAULT_CONFIG);

  const config = budget?.config ?? DEFAULT_CONFIG;
  const report = budget?.report;
  const complete = report?.complete === true;
  const active = report?.active === true;
  const unknownUsageRows = report?.unknown_usage_rows ?? 0;
  const knownCalls = report?.known_calls ?? 0;

  const openEdit = () => {
    setDraft({ ...config });
    setEditOpen(true);
  };

  const handleSave = () => {
    const invalid = validateForm(draft);
    if (invalid === "max_calls") {
      toast.error(t(($) => $.detail.mb_err_max_calls, { max: MAX_CALLS }));
      return;
    }
    if (invalid === "window_seconds") {
      toast.error(
        t(($) => $.detail.mb_err_window_seconds, {
          min: MIN_WINDOW_SECONDS,
          max: MAX_WINDOW_SECONDS,
        }),
      );
      return;
    }
    save.mutate(draft, {
      onSuccess: () => {
        setEditOpen(false);
        toast.success(t(($) => $.detail.mb_save_ok));
      },
      onError: (error) =>
        toast.error(
          error instanceof Error
            ? error.message
            : t(($) => $.detail.mb_save_failed),
        ),
    });
  };

  const state = !config.enabled
    ? t(($) => $.detail.mb_state_disabled)
    : active
      ? t(($) => $.detail.mb_state_active)
      : t(($) => $.detail.mb_state_tracking);

  return (
    <section
      data-testid="model-call-budget-card"
      className="rounded-lg border bg-card p-4"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="flex items-center gap-1.5 text-body font-medium">
            <Gauge className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
            {t(($) => $.detail.mb_card_title)}
          </h3>
          <p className="mt-0.5 text-caption text-muted-foreground">
            {t(($) => $.detail.mb_description)}
          </p>
        </div>
        {canEdit && (
          <Button
            variant="outline"
            size="sm"
            onClick={openEdit}
            data-testid="mb-edit"
          >
            <Pencil className="h-3.5 w-3.5" />
            {t(($) => $.detail.mb_edit)}
          </Button>
        )}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-caption sm:grid-cols-3">
        <Fact label={t(($) => $.detail.mb_state)}>{state}</Fact>
        <Fact label={t(($) => $.detail.mb_known_calls)}>{knownCalls}</Fact>
        <Fact label={t(($) => $.detail.mb_limit)}>{config.max_calls}</Fact>
        <Fact label={t(($) => $.detail.mb_window)}>
          {t(($) => $.detail.mb_window_value, { seconds: config.window_seconds })}
        </Fact>
        <Fact label={t(($) => $.detail.mb_unknown_rows)}>{unknownUsageRows}</Fact>
        <Fact label={t(($) => $.detail.mb_completeness)}>
          {complete
            ? t(($) => $.detail.mb_complete)
            : t(($) => $.detail.mb_incomplete)}
        </Fact>
      </div>

      {report?.window_started_at ? (
        <p className="mt-3 text-micro text-muted-foreground">
          {t(($) => $.detail.mb_window_started, {
            when: timeAgo(report.window_started_at),
          })}
        </p>
      ) : null}
      {active ? (
        <p className="mt-2 text-caption text-warning">
          {t(($) => $.detail.mb_active_hint)}
        </p>
      ) : !complete ? (
        <p className="mt-2 text-caption text-muted-foreground">
          {t(($) => $.detail.mb_incomplete_hint)}
        </p>
      ) : null}

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.detail.mb_edit_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.detail.mb_edit_description)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <Label htmlFor="mb-enabled">{t(($) => $.detail.mb_enabled)}</Label>
              <Switch
                id="mb-enabled"
                checked={draft.enabled}
                onCheckedChange={(enabled) =>
                  setDraft((current) => ({ ...current, enabled: enabled === true }))
                }
              />
            </div>
            <NumberField
              id="mb-max-calls"
              label={t(($) => $.detail.mb_max_calls)}
              hint={t(($) => $.detail.mb_max_calls_hint)}
              value={draft.max_calls}
              onChange={(max_calls) =>
                setDraft((current) => ({ ...current, max_calls }))
              }
            />
            <NumberField
              id="mb-window-seconds"
              label={t(($) => $.detail.mb_window_seconds)}
              hint={t(($) => $.detail.mb_window_seconds_hint)}
              value={draft.window_seconds}
              onChange={(window_seconds) =>
                setDraft((current) => ({ ...current, window_seconds }))
              }
            />
          </div>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setEditOpen(false)}>
              {t(($) => $.detail.mb_cancel)}
            </Button>
            <Button
              size="sm"
              onClick={handleSave}
              disabled={save.isPending}
              data-testid="mb-save"
            >
              {t(($) => $.detail.mb_save)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span className="text-micro uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span className="truncate font-mono text-caption text-foreground">{children}</span>
    </span>
  );
}

function NumberField({
  id,
  label,
  hint,
  value,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <div className="space-y-0.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        step="1"
        value={Number.isFinite(value) ? value : ""}
        onChange={(event) => {
          const next = Number(event.target.value);
          if (Number.isFinite(next)) onChange(next);
        }}
      />
      <p className="text-micro text-muted-foreground">{hint}</p>
    </div>
  );
}
