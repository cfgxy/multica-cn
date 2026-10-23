"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type {
  AgentBackpressureConfig,
  AgentBackpressurePolicy,
} from "@multica/core/api/schemas";
import type { Agent } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import {
  agentBackpressurePolicyOptions,
  agentBackpressureEventsOptions,
} from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberListOptions } from "@multica/core/workspace/queries";
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { toast } from "sonner";
import { useT } from "../../../i18n";

// RUYI-859 stage A: the agent-level error-rate backpressure config surface +
// read-only visibility card on the shared Agent detail page. It shows the
// effective policy, the current entry-window reading (terminal samples only,
// split by display class), the recovery-window figure against the FIXED 0.5x
// recovery threshold, the pause source (human or auto, with live queue
// depth), and the recent entered/cleared events. Writes are workspace-admin
// only; the automatic suppression loop itself is stage B — the switch here
// merely persists the intent that B will honor.

const CLASS_LABEL_KEYS = [
  "auth",
  "rate_limit",
  "timeout",
  "provider",
  "runtime",
  "agent",
  "misc",
] as const;

type ClassLabelKey = (typeof CLASS_LABEL_KEYS)[number];

function classLabel(
  cls: string,
  t: ReturnType<typeof useT<"agents">>["t"],
): string {
  const known: Record<ClassLabelKey, string> = {
    auth: t(($) => $.tab_body.backpressure.class_auth),
    rate_limit: t(($) => $.tab_body.backpressure.class_rate_limit),
    timeout: t(($) => $.tab_body.backpressure.class_timeout),
    provider: t(($) => $.tab_body.backpressure.class_provider),
    runtime: t(($) => $.tab_body.backpressure.class_runtime),
    agent: t(($) => $.tab_body.backpressure.class_agent),
    misc: t(($) => $.tab_body.backpressure.class_misc),
  };
  // A newer backend can send a display class this client has never seen:
  // fall back to the raw value so the figure stays visible.
  return (known[cls as ClassLabelKey] as string | undefined) ?? cls;
}

// Server built-in defaults when no policy row exists: monitor-only,
// automatic suppression off.
function effectiveConfig(
  config: AgentBackpressureConfig | null | undefined,
): AgentBackpressureConfig {
  return {
    window_minutes: config?.window_minutes ?? 60,
    error_rate_threshold_pct: config?.error_rate_threshold_pct ?? 30,
    min_samples: config?.min_samples ?? 5,
    recovery_window_minutes: config?.recovery_window_minutes ?? 30,
    suppress_on_error_rate: config?.suppress_on_error_rate ?? false,
    count_model_api_failures: config?.count_model_api_failures ?? true,
    count_execution_failures: config?.count_execution_failures ?? true,
  };
}

type CardNumberFields = Pick<
  AgentBackpressureConfig,
  | "window_minutes"
  | "error_rate_threshold_pct"
  | "min_samples"
  | "recovery_window_minutes"
>;

export function BackpressureTab({ agent }: { agent: Agent }) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const queryClient = useQueryClient();

  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!wsId,
  });
  // Writes to the policy card are workspace owner/admin only (the server's
  // authorizeSchedulingControl mirror) — the editor renders for them only.
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canEdit =
    currentMember?.role === "owner" || currentMember?.role === "admin";

  const { data: policy, isLoading } = useQuery({
    ...agentBackpressurePolicyOptions(wsId, agent.id),
  });
  const { data: events } = useQuery({
    ...agentBackpressureEventsOptions(wsId, agent.id),
  });

  const refresh = () =>
    queryClient.invalidateQueries({
      queryKey: agentBackpressurePolicyOptions(wsId, agent.id).queryKey,
    });

  const policyData = policy as AgentBackpressurePolicy | undefined;
  const effective = effectiveConfig(policyData?.config ?? null);
  const windowReading = policyData?.window ?? null;
  const scheduling = policyData?.scheduling ?? null;
  const shownEvents = events ?? policyData?.events ?? [];

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<CardNumberFields>(() => ({
    window_minutes: effective.window_minutes,
    error_rate_threshold_pct: effective.error_rate_threshold_pct,
    min_samples: effective.min_samples,
    recovery_window_minutes: effective.recovery_window_minutes,
  }));
  const [suppressedOn, setSuppressedOn] = useState(false);
  const [modelClassOn, setModelClassOn] = useState(true);
  const [execClassOn, setExecClassOn] = useState(true);

  const beginEdit = () => {
    setDraft({
      window_minutes: effective.window_minutes,
      error_rate_threshold_pct: effective.error_rate_threshold_pct,
      min_samples: effective.min_samples,
      recovery_window_minutes: effective.recovery_window_minutes,
    });
    setSuppressedOn(effective.suppress_on_error_rate);
    setModelClassOn(effective.count_model_api_failures);
    setExecClassOn(effective.count_execution_failures);
    setEditing(true);
  };

  const saveMutation = useMutation({
    mutationFn: (config: AgentBackpressureConfig) =>
      api.putAgentBackpressurePolicy(agent.id, config),
    onSuccess: () => {
      setEditing(false);
      toast.success(t(($) => $.tab_body.backpressure.saved_toast));
      refresh();
    },
    onError: () =>
      toast.error(t(($) => $.tab_body.backpressure.save_failed_toast)),
  });

  const resetMutation = useMutation({
    mutationFn: () => api.deleteAgentBackpressurePolicy(agent.id),
    onSuccess: () => {
      setEditing(false);
      toast.success(t(($) => $.tab_body.backpressure.reset_toast));
      refresh();
    },
    onError: () =>
      toast.error(t(($) => $.tab_body.backpressure.reset_failed_toast)),
  });

  const setNumber = (key: keyof CardNumberFields, value: string) => {
    const parsed = Number(value);
    setDraft((prev) => ({
      ...prev,
      [key]: Number.isFinite(parsed) ? parsed : 0,
    }));
  };

  const isDirty =
    draft.window_minutes !== effective.window_minutes ||
    draft.error_rate_threshold_pct !== effective.error_rate_threshold_pct ||
    draft.min_samples !== effective.min_samples ||
    draft.recovery_window_minutes !== effective.recovery_window_minutes ||
    suppressedOn !== effective.suppress_on_error_rate ||
    modelClassOn !== effective.count_model_api_failures ||
    execClassOn !== effective.count_execution_failures;

  const save = () => {
    saveMutation.mutate({
      window_minutes: draft.window_minutes,
      error_rate_threshold_pct: draft.error_rate_threshold_pct,
      min_samples: draft.min_samples,
      recovery_window_minutes: draft.recovery_window_minutes,
      suppress_on_error_rate: suppressedOn,
      count_model_api_failures: modelClassOn,
      count_execution_failures: execClassOn,
    });
  };

  if (isLoading) {
    return (
      <div data-testid="agent-backpressure-tab" className="space-y-4">
        <Alert>
          <AlertTitle>
            {t(($) => $.tab_body.backpressure.loading_title)}
          </AlertTitle>
          <AlertDescription>
            {t(($) => $.tab_body.backpressure.loading_body)}
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  const entryReading = windowReading;
  const sampleCount = entryReading?.total_terminal ?? 0;

  return (
    <div data-testid="agent-backpressure-tab" className="space-y-6">
      <p className="text-body text-muted-foreground">
        {t(($) => $.tab_body.backpressure.intro)}
      </p>

      {/* The two windows and the FIXED 0.5x recovery threshold are product
          semantics the card must always surface (RUYI-859 stage A). */}
      <section className="space-y-3">
        <h3 className="text-title-sm font-medium">
          {t(($) => $.tab_body.backpressure.windows_title)}
        </h3>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border p-3">
            <div className="text-caption text-muted-foreground">
              {t(($) => $.tab_body.backpressure.entry_window)}
            </div>
            <div className="text-title font-medium">
              {sampleCount} {t(($) => $.tab_body.backpressure.terminal_samples)}
            </div>
            <div className="mt-1 text-caption text-muted-foreground">
              {t(($) => $.tab_body.backpressure.entry_window_hint, {
                minutes: effective.window_minutes,
                threshold: effective.error_rate_threshold_pct,
                min: effective.min_samples,
              })}
            </div>
          </div>
          <div className="rounded-lg border p-3">
            <div className="text-caption text-muted-foreground">
              {t(($) => $.tab_body.backpressure.recovery_window)}
            </div>
            <div className="text-title font-medium">
              {t(($) => $.tab_body.backpressure.recovery_threshold_line, {
                threshold:
                  Math.round(effective.error_rate_threshold_pct * 0.5 * 10) /
                  10,
                minutes: effective.recovery_window_minutes,
                min: effective.min_samples,
              })}
            </div>
            <div className="mt-1 text-caption text-muted-foreground">
              {t(($) => $.tab_body.backpressure.recovery_window_hint)}
            </div>
          </div>
        </div>

        {sampleCount === 0 ? (
          <Alert>
            <AlertTitle>
              {t(($) => $.tab_body.backpressure.no_samples_title)}
            </AlertTitle>
            <AlertDescription>
              {t(($) => $.tab_body.backpressure.no_samples_hint)}
            </AlertDescription>
          </Alert>
        ) : (
          <div className="rounded-lg border p-3">
            <div className="text-caption text-muted-foreground">
              {t(($) => $.tab_body.backpressure.current_rate)}
            </div>
            <div className="text-title font-medium">
              {entryReading?.error_rate_pct?.toFixed(1) ?? "0.0"}%
            </div>
            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
              {Object.entries(entryReading?.by_class ?? {}).map(([cls, n]) =>
                n === 0 ? null : (
                  <span key={cls} className="text-caption">
                    {classLabel(cls, t)}: {n}
                  </span>
                ),
              )}
            </div>
          </div>
        )}
      </section>

      {canEdit && (
        <section className="space-y-3">
          <h3 className="text-title-sm font-medium">
            {t(($) => $.tab_body.backpressure.policy_title)}
          </h3>
          {!policyData?.configured && !editing && (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.tab_body.backpressure.defaults_marker)}
            </p>
          )}
          {editing ? (
            <div className="space-y-4 rounded-lg border p-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1">
                  <Label htmlFor="bp-window">
                    {t(($) => $.tab_body.backpressure.field_window)}
                  </Label>
                  <Input
                    id="bp-window"
                    type="number"
                    min={1}
                    value={draft.window_minutes}
                    onChange={(e) =>
                      setNumber("window_minutes", e.target.value)
                    }
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="bp-threshold">
                    {t(($) => $.tab_body.backpressure.field_threshold)}
                  </Label>
                  <Input
                    id="bp-threshold"
                    type="number"
                    min={0}
                    max={100}
                    step="0.1"
                    value={draft.error_rate_threshold_pct}
                    onChange={(e) =>
                      setNumber("error_rate_threshold_pct", e.target.value)
                    }
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="bp-min">
                    {t(($) => $.tab_body.backpressure.field_min_samples)}
                  </Label>
                  <Input
                    id="bp-min"
                    type="number"
                    min={1}
                    value={draft.min_samples}
                    onChange={(e) => setNumber("min_samples", e.target.value)}
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="bp-recovery">
                    {t(($) => $.tab_body.backpressure.field_recovery_window)}
                  </Label>
                  <Input
                    id="bp-recovery"
                    type="number"
                    min={1}
                    value={draft.recovery_window_minutes}
                    onChange={(e) =>
                      setNumber("recovery_window_minutes", e.target.value)
                    }
                  />
                </div>
              </div>
              <div className="space-y-2">
                <div className="flex items-center justify-between gap-3">
                  <Label htmlFor="bp-suppress">
                    {t(($) => $.tab_body.backpressure.field_suppress)}
                  </Label>
                  <Switch
                    id="bp-suppress"
                    checked={suppressedOn}
                    onCheckedChange={(v) => setSuppressedOn(v === true)}
                  />
                </div>
                <div className="flex items-center justify-between gap-3">
                  <Label htmlFor="bp-model-class">
                    {t(($) => $.tab_body.backpressure.class_switch_model)}
                  </Label>
                  <Switch
                    id="bp-model-class"
                    checked={modelClassOn}
                    onCheckedChange={(v) => setModelClassOn(v === true)}
                  />
                </div>
                <div className="flex items-center justify-between gap-3">
                  <Label htmlFor="bp-exec-class">
                    {t(($) => $.tab_body.backpressure.class_switch_exec)}
                  </Label>
                  <Switch
                    id="bp-exec-class"
                    checked={execClassOn}
                    onCheckedChange={(v) => setExecClassOn(v === true)}
                  />
                </div>
              </div>
              <div className="flex justify-end gap-2">
                <Button
                  type="button"
                  variant="ghost"
                  disabled={saveMutation.isPending}
                  onClick={() => setEditing(false)}
                >
                  {t(($) => $.tab_body.backpressure.cancel_action)}
                </Button>
                <Button
                  type="button"
                  onClick={save}
                  disabled={saveMutation.isPending || !isDirty}
                >
                  {t(($) => $.tab_body.backpressure.save_action)}
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-3">
              <div className="text-caption text-muted-foreground">
                {t(($) => $.tab_body.backpressure.suppress_state, {
                  on: effective.suppress_on_error_rate ? "on" : "off",
                })}
                {" · "}
                {t(($) => $.tab_body.backpressure.class_criteria, {
                  model: effective.count_model_api_failures ? "on" : "off",
                  exec: effective.count_execution_failures ? "on" : "off",
                })}
              </div>
              <div className="flex justify-end gap-2">
                {policyData?.configured && (
                  <Button
                    type="button"
                    variant="ghost"
                    data-testid="bp-reset"
                    onClick={() => resetMutation.mutate()}
                    disabled={resetMutation.isPending}
                  >
                    {t(($) => $.tab_body.backpressure.reset_action)}
                  </Button>
                )}
                <Button
                  type="button"
                  variant="secondary"
                  data-testid="bp-edit"
                  onClick={beginEdit}
                >
                  {t(($) => $.tab_body.backpressure.edit_action)}
                </Button>
              </div>
            </div>
          )}
        </section>
      )}

      <section className="space-y-3">
        <h3 className="text-title-sm font-medium">
          {t(($) => $.tab_body.backpressure.pause_source_title)}
        </h3>
        <div className="rounded-lg border p-3">
          <div className="text-body">
            {scheduling?.paused
              ? t(($) => $.tab_body.backpressure.paused_now, {
                  scope: scheduling.scope ?? "agent",
                })
              : t(($) => $.tab_body.backpressure.not_paused)}
          </div>
          {scheduling?.paused && (
            <div className="mt-1 text-caption text-muted-foreground">
              {/* Stage B's auto-suppression loop will name its own reason
                  here; today every pause source is a human freeze (agent or
                  workspace scope). The created_by field is raw on purpose:
                  a system-generated marker must not be mistranslated. */}
              {scheduling.reason || scheduling.created_by
                ? `${scheduling.reason ?? scheduling.created_by} · ${scheduling.created_at ?? ""}`
                : ""}
            </div>
          )}
          <div className="mt-2 text-caption text-muted-foreground">
            {t(($) => $.tab_body.backpressure.queued_depth, {
              count: scheduling?.queued_count ?? 0,
            })}
          </div>
        </div>
      </section>

      <section className="space-y-3">
        <h3 className="text-title-sm font-medium">
          {t(($) => $.tab_body.backpressure.events_title)}
        </h3>
        {shownEvents.length === 0 ? (
          <p className="text-caption text-muted-foreground">
            {t(($) => $.tab_body.backpressure.events_empty)}
          </p>
        ) : (
          <ul className="space-y-2">
            {shownEvents.map((evt) => (
              <li
                key={evt.id}
                className="rounded-lg border p-3 text-caption text-muted-foreground"
              >
                <span className="font-medium text-foreground">
                  {evt.event_type === "entered"
                    ? t(($) => $.tab_body.backpressure.event_entered)
                    : t(($) => $.tab_body.backpressure.event_cleared)}
                </span>
                {" · "}
                {t(($) => $.tab_body.backpressure.event_line, {
                  samples: evt.sample_count,
                  rate: evt.error_rate_pct.toFixed(1),
                  when: evt.occurred_at,
                })}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
