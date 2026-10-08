"use client";

import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import type { AgentRuntime } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { runtimeDisplayName } from "@multica/core/runtimes";
import {
  usePutRuntimeCredential,
  useDeleteRuntimeCredential,
  useUpdateRuntime,
} from "@multica/core/runtimes/mutations";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../../i18n";

/**
 * RUYI-425 §4.3 — Gemini Live instance settings card (stage 2, desktop).
 *
 * Mounted on the runtime detail page for voice-protocol instances only. The
 * instance form is an EDIT surface: manual instance *creation* is the stage-3
 * registration flow, so this card ships on existing instances. Fields:
 * display name (duplicates allowed), read-only type / registration /
 * workspace visibility, the API key (password control + update button +
 * tri-state badge — the plaintext never echoes back), the model (default
 * gemini-3.8-live), the collapsed advanced-params JSON, and the enable
 * toggle. Saving or rotating the key triggers the server's lightweight
 * connectivity probe; a failed probe surfaces as a warning and the badge —
 * it never blocks the save (§4.5).
 *
 * Boundary note (§8 / stage-2 dispatch): the values persist through the
 * existing runtime PATCH endpoint's voice-settings extension
 * (`model` / `advanced` / `disabled` merged into instance metadata) — no new
 * write path. The enable toggle therefore lands in metadata (`disabled`),
 * because agent_runtime.status only admits online/offline.
 */

/** The credential key this form manages (mirrors the Go handler tests). */
export const VOICE_INSTANCE_CREDENTIAL_KEY = "api_key";

/**
 * Whether the runtime is a voice-protocol instance. The server derives
 * `capabilities` from the instance's protocol family baseline; voice
 * instances can only exist on a Stage-1+ backend, which always populates the
 * field, so a missing block (older backend / CLI instance) means "not voice".
 */
export function isVoiceProtocolRuntime(runtime: AgentRuntime): boolean {
  return runtime.capabilities?.realtime_voice === true;
}

export interface VoiceInstanceSettings {
  model: string;
  advanced: Record<string, unknown> | null;
  disabled: boolean;
}

/** Reads the §4.3 settings out of the instance metadata bag. */
export function readVoiceInstanceSettings(
  metadata: Record<string, unknown> | null | undefined,
): VoiceInstanceSettings {
  const bag = metadata ?? {};
  return {
    model: typeof bag.model === "string" ? bag.model : "",
    advanced:
      bag.advanced && typeof bag.advanced === "object" && !Array.isArray(bag.advanced)
        ? (bag.advanced as Record<string, unknown>)
        : null,
    disabled: bag.disabled === true,
  };
}

export type AdvancedParamsResult =
  | { ok: true; value: Record<string, unknown> }
  | { ok: false; reason: "invalid_json" | "not_object" };

/**
 * Advanced params: JSON-object text → PATCH value. Empty text clears the
 * stored object; anything that is not a JSON object fails closed — the
 * server would store it, but a scalar/array here is always a user mistake.
 */
export function parseAdvancedParams(text: string): AdvancedParamsResult {
  const trimmed = text.trim();
  if (trimmed === "") return { ok: true, value: {} };
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return { ok: false, reason: "invalid_json" };
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return { ok: false, reason: "not_object" };
  }
  return { ok: true, value: parsed as Record<string, unknown> };
}

export function VoiceInstanceSettingsCard({
  runtime,
  canEdit,
}: {
  runtime: AgentRuntime;
  canEdit: boolean;
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const updateRuntime = useUpdateRuntime(wsId);
  const putCredential = usePutRuntimeCredential(wsId);
  const deleteCredential = useDeleteRuntimeCredential(wsId);

  const settings = readVoiceInstanceSettings(runtime.metadata);

  const [name, setName] = useState(() => runtimeDisplayName(runtime));
  const [model, setModel] = useState(settings.model);
  const [advancedText, setAdvancedText] = useState(() =>
    settings.advanced ? JSON.stringify(settings.advanced, null, 2) : "",
  );
  const [keyValue, setKeyValue] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);

  // Re-seed when the invalidated queries deliver fresh instance data (any
  // successful save flips the runtime object) or when switching instances.
  // Deps are the server values, never the local drafts, so background
  // refetches don't clobber in-flight typing.
  const advancedJson = useMemo(() => {
    const advanced = readVoiceInstanceSettings(runtime.metadata).advanced;
    return advanced ? JSON.stringify(advanced, null, 2) : "";
  }, [runtime.metadata]);
  useEffect(() => {
    // RUYI-564: seed with the display name (custom_name first, else name —
    // runtimeDisplayName, mobile RUYI-540 parity). Create-only instances
    // carry no custom_name; seeding only from it left the field blank.
    setName(runtimeDisplayName(runtime));
  }, [runtime.id, runtime.custom_name, runtime.name]);
  useEffect(() => {
    setModel(settings.model);
  }, [runtime.id, settings.model]);
  useEffect(() => {
    setAdvancedText(advancedJson);
  }, [runtime.id, advancedJson]);

  const showError = (err: unknown, fallback: string) =>
    toast.error(
      err instanceof Error && err.message ? err.message : fallback,
    );

  const saveName = () => {
    const next = name.trim();
    if (next === (runtime.custom_name ?? "")) return;
    updateRuntime.mutate(
      { runtimeId: runtime.id, patch: { custom_name: next } },
      {
        onSuccess: () => toast.success(t(($) => $.voice_instance.name_saved)),
        onError: (err) =>
          showError(err, t(($) => $.voice_instance.save_failed)),
      },
    );
  };

  const saveModel = () => {
    if (model === settings.model) return;
    updateRuntime.mutate(
      { runtimeId: runtime.id, patch: { model } },
      {
        onSuccess: () => toast.success(t(($) => $.voice_instance.model_saved)),
        onError: (err) =>
          showError(err, t(($) => $.voice_instance.save_failed)),
      },
    );
  };

  const saveAdvanced = () => {
    const parsed = parseAdvancedParams(advancedText);
    if (!parsed.ok) {
      toast.error(t(($) => $.voice_instance.advanced_invalid));
      return;
    }
    if (
      JSON.stringify(parsed.value) === JSON.stringify(settings.advanced ?? {})
    ) {
      return;
    }
    updateRuntime.mutate(
      { runtimeId: runtime.id, patch: { advanced: parsed.value } },
      {
        onSuccess: () =>
          toast.success(t(($) => $.voice_instance.advanced_saved)),
        onError: (err) =>
          showError(err, t(($) => $.voice_instance.save_failed)),
      },
    );
  };

  const updateKey = () => {
    const value = keyValue;
    if (!value.trim()) return;
    putCredential.mutate(
      {
        runtimeId: runtime.id,
        credentialKey: VOICE_INSTANCE_CREDENTIAL_KEY,
        value,
      },
      {
        onSuccess: (res) => {
          setKeyValue("");
          if (res.probe?.status === "invalid") {
            // Saved is saved (§4.5): the probe never blocks the write — it
            // only downgrades the toast and flips the badge via the
            // invalidated instance queries.
            toast.warning(
              t(($) => $.voice_instance.probe_invalid, {
                status: res.probe?.http_status ?? "",
              }),
            );
          } else {
            toast.success(t(($) => $.voice_instance.key_saved));
          }
        },
        onError: (err) =>
          showError(err, t(($) => $.voice_instance.key_update_failed)),
      },
    );
  };

  const clearKey = () => {
    deleteCredential.mutate(
      {
        runtimeId: runtime.id,
        credentialKey: VOICE_INSTANCE_CREDENTIAL_KEY,
      },
      {
        onSuccess: () => toast.success(t(($) => $.voice_instance.key_cleared)),
        onError: (err) =>
          showError(err, t(($) => $.voice_instance.key_update_failed)),
      },
    );
  };

  const toggleEnabled = (enabled: boolean) => {
    updateRuntime.mutate(
      { runtimeId: runtime.id, patch: { disabled: !enabled } },
      {
        onError: (err) =>
          showError(err, t(($) => $.voice_instance.save_failed)),
      },
    );
  };

  const pending =
    updateRuntime.isPending || putCredential.isPending || deleteCredential.isPending;

  return (
    <div className="rounded-lg border bg-card" data-testid="voice-instance-settings">
      <div className="flex items-center justify-between border-b px-4 py-3">
        <span className="text-caption font-semibold">
          {t(($) => $.voice_instance.title)}
        </span>
        <CredentialBadge
          status={runtime.credential_status ?? "not_configured"}
        />
      </div>

      <div className="space-y-4 p-4">
        {/* 名称 — duplicates allowed (display override, not an identity). */}
        <div className="space-y-1.5">
          <Label htmlFor="voice-instance-name">
            {t(($) => $.voice_instance.name_label)}
          </Label>
          {canEdit ? (
            <div className="flex gap-2">
              <Input
                id="voice-instance-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={pending}
                className="h-8"
              />
              <Button
                variant="outline"
                size="sm"
                className="h-8 shrink-0"
                disabled={
                  pending ||
                  !name.trim() ||
                  name.trim() === (runtime.custom_name ?? "")
                }
                onClick={saveName}
              >
                {t(($) => $.voice_instance.save)}
              </Button>
            </div>
          ) : (
            <ReadonlyValue value={runtimeDisplayName(runtime)} />
          )}
        </div>

        {/* Type — read-only in edit mode (§4.3). */}
        <div className="space-y-1.5">
          <Label>{t(($) => $.voice_instance.type_label)}</Label>
          <ReadonlyValue
            value={runtime.protocol_family || runtime.provider}
            mono
          />
        </div>

        {/* API Key — password control, never echoes the stored value. */}
        {canEdit && (
          <div className="space-y-1.5">
            <Label htmlFor="voice-instance-key">
              {t(($) => $.voice_instance.key_label)}
            </Label>
            <div className="flex gap-2">
              <Input
                id="voice-instance-key"
                type="password"
                value={keyValue}
                onChange={(e) => setKeyValue(e.target.value)}
                placeholder={t(($) => $.voice_instance.key_placeholder)}
                autoComplete="off"
                disabled={pending}
                className="h-8"
              />
              <Button
                variant="outline"
                size="sm"
                className="h-8 shrink-0"
                disabled={pending || !keyValue.trim()}
                onClick={updateKey}
              >
                {t(($) => $.voice_instance.key_update)}
              </Button>
              {(runtime.credential_status ?? "not_configured") !==
                "not_configured" && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-8 shrink-0 text-destructive hover:bg-destructive/10 hover:text-destructive"
                  disabled={pending}
                  onClick={clearKey}
                >
                  {t(($) => $.voice_instance.key_clear)}
                </Button>
              )}
            </div>
            <p className="text-micro text-muted-foreground">
              {t(($) => $.voice_instance.key_hint)}
            </p>
          </div>
        )}

        {/* 模型 — defaults to gemini-3.8-live when unset. */}
        <div className="space-y-1.5">
          <Label htmlFor="voice-instance-model">
            {t(($) => $.voice_instance.model_label)}
          </Label>
          {canEdit ? (
            <div className="flex gap-2">
              <Input
                id="voice-instance-model"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder={t(($) => $.voice_instance.model_placeholder)}
                disabled={pending}
                className="h-8 font-mono"
              />
              <Button
                variant="outline"
                size="sm"
                className="h-8 shrink-0"
                disabled={pending || model === settings.model}
                onClick={saveModel}
              >
                {t(($) => $.voice_instance.save)}
              </Button>
            </div>
          ) : (
            <ReadonlyValue value={settings.model || "—"} mono />
          )}
        </div>

        {/* 高级参数 — collapsed JSON block (§4.3). */}
        <div className="space-y-1.5">
          <button
            type="button"
            disabled={!canEdit}
            onClick={() => setAdvancedOpen((v) => !v)}
            className="text-caption font-medium text-foreground underline-offset-2 hover:underline disabled:no-underline disabled:opacity-80"
          >
            {t(($) => $.voice_instance.advanced_label)}
          </button>
          {canEdit ? (
            advancedOpen && (
              <div className="space-y-2">
                <Textarea
                  value={advancedText}
                  onChange={(e) => setAdvancedText(e.target.value)}
                  rows={4}
                  placeholder={t(($) => $.voice_instance.advanced_placeholder)}
                  disabled={pending}
                  className="font-mono text-caption"
                />
                <Button
                  variant="outline"
                  size="sm"
                  className="h-8"
                  disabled={pending}
                  onClick={saveAdvanced}
                >
                  {t(($) => $.voice_instance.save)}
                </Button>
              </div>
            )
          ) : (
            <ReadonlyValue
              value={
                settings.advanced
                  ? JSON.stringify(settings.advanced)
                  : "—"
              }
              mono
            />
          )}
        </div>

        {/* 注册方式 — read-only (§4.3): manual for voice instances. */}
        <div className="space-y-1.5">
          <Label>{t(($) => $.voice_instance.registration_label)}</Label>
          <ReadonlyValue
            value={
              runtime.registration_source === "manual"
                ? t(($) => $.voice_instance.registration_manual)
                : t(($) => $.voice_instance.registration_daemon)
            }
          />
        </div>

        {/* 启用 — persists via metadata.disabled (status stays online/offline). */}
        {canEdit && (
          <div className="flex items-center justify-between gap-3 border-t pt-3">
            <div className="min-w-0">
              <Label htmlFor="voice-instance-enabled" className="text-body font-medium">
                {t(($) => $.voice_instance.enabled_label)}
              </Label>
              <p className="text-micro text-muted-foreground">
                {t(($) => $.voice_instance.enabled_desc)}
              </p>
            </div>
            <Switch
              id="voice-instance-enabled"
              checked={!settings.disabled}
              disabled={pending}
              onCheckedChange={toggleEnabled}
            />
          </div>
        )}

        {/* 可见性 — fixed to workspace for voice instances (§4.3). */}
        <div className="space-y-1.5">
          <Label>{t(($) => $.voice_instance.visibility_label)}</Label>
          <ReadonlyValue
            value={t(($) => $.voice_instance.visibility_workspace)}
          />
        </div>
      </div>
    </div>
  );
}

function CredentialBadge({
  status,
}: {
  status: "not_configured" | "configured" | "invalid";
}) {
  const { t } = useT("runtimes");
  if (status === "configured") {
    return (
      <Badge variant="outline" className="text-success">
        {t(($) => $.voice_instance.badge_configured)}
      </Badge>
    );
  }
  if (status === "invalid") {
    return (
      <Badge variant="outline" className="text-destructive">
        {t(($) => $.voice_instance.badge_invalid)}
      </Badge>
    );
  }
  return (
    <Badge variant="outline" className="text-muted-foreground">
      {t(($) => $.voice_instance.badge_not_configured)}
    </Badge>
  );
}

function ReadonlyValue({
  value,
  mono,
}: {
  value: string;
  mono?: boolean;
}) {
  return (
    <span
      className={`block truncate rounded-md border bg-muted/30 px-2 py-1.5 text-caption ${
        mono ? "font-mono" : ""
      }`}
    >
      {value}
    </span>
  );
}
