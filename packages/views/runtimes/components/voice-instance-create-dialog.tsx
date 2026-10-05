"use client";

import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { AgentRuntime, RuntimeProfile } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  runtimeProfileListOptions,
  runtimeProfileKeys,
  useCreateRuntimeProfile,
} from "@multica/core/runtimes";
import {
  useCreateManualRuntime,
  usePutRuntimeCredential,
} from "@multica/core/runtimes/mutations";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
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
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../../i18n";
import {
  VOICE_INSTANCE_CREDENTIAL_KEY,
  parseAdvancedParams,
} from "./voice-instance-settings";

/**
 * RUYI-425 §4.3 — Gemini Live instance creation dialog (stage 3, desktop).
 *
 * The registration half of the §4.3 form: name (duplicates allowed), the
 * voice-family profile (Type), the API key (stored encrypted right after
 * create — the save triggers the server's connectivity probe), the model
 * (placeholder gemini-3.8-live), and the collapsed advanced-params JSON.
 * Registration (manual) and workspace visibility are server-fixed, so they
 * render read-only. The enable toggle is post-create (settings card).
 *
 * A workspace without any voice profile gets a one-click default profile
 * (gemini_live needs no command_name) instead of a dead-end empty select.
 */

/** The profiles the create form offers, derived from Type capabilities. */
export function isVoiceProfile(profile: RuntimeProfile): boolean {
  return profile.capabilities?.realtime_voice === true;
}

export function VoiceInstanceCreateDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  /** Called with the new instance after create + credential + probe. */
  onCreated?: (runtime: AgentRuntime) => void;
}) {
  const { t } = useT("runtimes");
  const { t: tc } = useT("common");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const createRuntime = useCreateManualRuntime(wsId);
  const putCredential = usePutRuntimeCredential(wsId);
  const createProfile = useCreateRuntimeProfile(wsId);
  const { data: profiles = [] } = useQuery(runtimeProfileListOptions(wsId));

  const [name, setName] = useState("");
  const [profileId, setProfileId] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState("");
  const [advancedText, setAdvancedText] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const voiceProfiles = useMemo(() => profiles.filter(isVoiceProfile), [profiles]);

  const effectiveProfileId = profileId || voiceProfiles[0]?.id || "";
  const trimmedName = name.trim();
  const advanced = parseAdvancedParams(advancedText);
  const canSubmit =
    trimmedName.length > 0 &&
    effectiveProfileId !== "" &&
    !submitting &&
    advanced.ok;

  const submit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    try {
      const body: Parameters<typeof createRuntime.mutateAsync>[0] = {
        name: trimmedName,
        profile_id: effectiveProfileId,
      };
      if (model.trim() !== "") body.model = model.trim();
      if (advancedText.trim() !== "" && advanced.ok && Object.keys(advanced.value).length > 0) {
        body.advanced = advanced.value;
      }
      const runtime = await createRuntime.mutateAsync(body);

      // The key is a separate encrypted store (§4.5); saving it triggers the
      // §4.3 connectivity probe server-side. A failed PUT leaves the instance
      // in place — the settings card can retry — so the dialog still closes.
      if (apiKey.trim() !== "") {
        try {
          const result = await putCredential.mutateAsync({
            runtimeId: runtime.id,
            credentialKey: VOICE_INSTANCE_CREDENTIAL_KEY,
            value: apiKey.trim(),
          });
          if (result.probe?.status === "invalid") {
            toast.warning(t(($) => $.voice_instance_create.created_probe_invalid));
          } else {
            toast.success(t(($) => $.voice_instance_create.created));
          }
        } catch {
          toast.warning(t(($) => $.voice_instance_create.key_save_failed));
        }
      } else {
        toast.success(t(($) => $.voice_instance_create.created));
      }
      onCreated?.(runtime);
      onClose();
    } catch {
      toast.error(t(($) => $.voice_instance_create.create_failed));
    } finally {
      setSubmitting(false);
    }
  };

  const createDefaultProfile = () =>
    createProfile.mutate(
      // Voice families are API-enforced command-less: an empty command_name
      // is exactly what the server requires for gemini_live profiles.
      { display_name: "Gemini Live", protocol_family: "gemini_live", command_name: "" },
      {
        onSuccess: (profile) => {
          setProfileId(profile.id);
          qc.invalidateQueries({ queryKey: runtimeProfileKeys.list(wsId) });
        },
        onError: () => {
          toast.error(t(($) => $.voice_instance_create.create_failed));
        },
      },
    );

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>{t(($) => $.voice_instance_create.title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.voice_instance_create.description)}
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label htmlFor="voice-instance-name">
              {t(($) => $.voice_instance.name_label)}
            </Label>
            <Input
              id="voice-instance-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={t(($) => $.voice_instance_create.name_placeholder)}
              maxLength={200}
            />
          </div>

          <div className="grid gap-2">
            <Label htmlFor="voice-instance-type">
              {t(($) => $.voice_instance.type_label)}
            </Label>
            {voiceProfiles.length === 0 ? (
              <div className="flex items-center justify-between gap-2 rounded-md border border-dashed p-3">
                <span className="text-sm text-muted-foreground">
                  {t(($) => $.voice_instance_create.no_profiles)}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={createProfile.isPending}
                  onClick={createDefaultProfile}
                >
                  {t(($) => $.voice_instance_create.create_default_profile)}
                </Button>
              </div>
            ) : (
              <Select
                items={voiceProfiles.map((profile) => ({
                  label: profile.display_name,
                  value: profile.id,
                }))}
                value={effectiveProfileId}
                onValueChange={(value) => {
                  if (value) setProfileId(value);
                }}
              >
                <SelectTrigger id="voice-instance-type" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {voiceProfiles.map((profile) => (
                    <SelectItem key={profile.id} value={profile.id}>
                      {profile.display_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          <div className="grid gap-2">
            <Label htmlFor="voice-instance-key">
              {t(($) => $.voice_instance.key_label)}
            </Label>
            <Input
              id="voice-instance-key"
              type="password"
              value={apiKey}
              onChange={(event) => setApiKey(event.target.value)}
              placeholder={t(($) => $.voice_instance_create.key_placeholder)}
              autoComplete="off"
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.voice_instance.key_hint)}
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="voice-instance-model">
              {t(($) => $.voice_instance.model_label)}
            </Label>
            <Input
              id="voice-instance-model"
              value={model}
              onChange={(event) => setModel(event.target.value)}
              placeholder={t(($) => $.voice_instance.model_placeholder)}
              maxLength={200}
            />
          </div>

          <details className="rounded-md border p-3">
            <summary className="cursor-pointer text-sm font-medium">
              {t(($) => $.voice_instance.advanced_label)}
            </summary>
            <div className="grid gap-2 pt-2">
              <Textarea
                id="voice-instance-advanced"
                rows={3}
                aria-label={t(($) => $.voice_instance.advanced_label)}
                value={advancedText}
                onChange={(event) => setAdvancedText(event.target.value)}
                placeholder={t(($) => $.voice_instance.advanced_placeholder)}
              />
              {!advanced.ok && (
                <p className="text-xs text-destructive">
                  {t(($) => $.voice_instance.advanced_invalid)}
                </p>
              )}
            </div>
          </details>

          <div className="grid grid-cols-2 gap-2 text-sm">
            <div>
              <span className="text-muted-foreground">
                {t(($) => $.voice_instance.registration_label)}:{" "}
              </span>
              <span>{t(($) => $.voice_instance.registration_manual)}</span>
            </div>
            <div>
              <span className="text-muted-foreground">
                {t(($) => $.voice_instance.visibility_label)}:{" "}
              </span>
              <span>{t(($) => $.voice_instance.visibility_workspace)}</span>
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {tc(($) => $.cancel)}
          </Button>
          <Button type="button" disabled={!canSubmit} onClick={submit}>
            {submitting
              ? t(($) => $.voice_instance_create.creating)
              : t(($) => $.voice_instance_create.submit)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
