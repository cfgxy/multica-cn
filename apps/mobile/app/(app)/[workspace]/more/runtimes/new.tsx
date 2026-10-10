/**
 * Register a voice instance (`more/runtimes/new`, RUYI-425 §4.3 mobile) —
 * modal create form, RN mirror of the desktop VoiceInstanceCreateDialog
 * (packages/views/runtimes/components/voice-instance-create-dialog.tsx).
 * views components are outside the mobile import whitelist, so the form is
 * native-built but every semantic is desktop parity:
 *
 * - Type lists ONLY voice-family profiles (`isVoiceProfile`); a workspace
 *   with none gets the one-click default profile (gemini_live needs no
 *   command_name) instead of a dead-end empty picker.
 * - Name duplicates are allowed (the row id is the identity).
 * - The API key is stored through the credential PUT right after create —
 *   that PUT triggers the server's §4.3 connectivity probe, surfacing the
 *   tri-state feedback: created / created-but-probe-invalid / registered-
 *   but-key-save-failed. A failed key save never deletes the instance.
 * - Advanced params must parse to a JSON object — bad JSON disables submit.
 * - Registration (manual) and visibility (workspace/public) are
 *   server-fixed, so they render read-only; the enable toggle lives on the
 *   settings page (post-create).
 *
 * Success navigates to the instance's settings page, where the fixed
 * manual/online/public presentation is visible (desktop closes the dialog
 * back onto the runtime list).
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router } from "expo-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  VOICE_INSTANCE_CREDENTIAL_KEY,
  credentialSaveFailureDetail,
  isVoiceProfile,
  parseAdvancedParams,
} from "@/lib/voice-runtime";
import type { RuntimeProfile } from "@multica/core/types";
import { probeVoiceCredential, type VoiceProbeOutcome } from "@/lib/voice/probe";
import { api } from "@/data/api";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { runtimeProfileKeys, runtimeProfileListOptions } from "@/data/queries/runtimes";
import {
  useCreateManualRuntime,
  useCreateRuntimeProfile,
  usePutRuntimeCredential,
} from "@/data/mutations/runtimes";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function NewVoiceRuntimeScreen() {
  const insets = useSafeAreaInsets();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((s) => s.currentWorkspaceSlug);
  const { t } = useT("runtimes");
  const { t: tc } = useT("common");
  const qc = useQueryClient();
  const createRuntime = useCreateManualRuntime(wsId);
  const putCredential = usePutRuntimeCredential(wsId);
  const createProfile = useCreateRuntimeProfile(wsId);
  const { data: profiles = [] } = useQuery(runtimeProfileListOptions(wsId ?? ""));

  const [name, setName] = useState("");
  const [profileId, setProfileId] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState("");
  const [advancedText, setAdvancedText] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);
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
    if (!canSubmit || !wsId) return;
    setSubmitting(true);
    try {
      const body: Parameters<typeof createRuntime.mutateAsync>[0] = {
        name: trimmedName,
        profile_id: effectiveProfileId,
      };
      if (model.trim() !== "") body.model = model.trim();
      if (
        advancedText.trim() !== "" &&
        advanced.ok &&
        Object.keys(advanced.value).length > 0
      ) {
        body.advanced = advanced.value;
      }
      const runtime = await createRuntime.mutateAsync(body);

      // The key is a separate encrypted store (§4.5); saving it triggers the
      // §4.3 connectivity probe server-side. A failed PUT leaves the
      // instance in place — the settings page can retry — so navigation
      // still proceeds (desktop closes the dialog in this branch too).
      let probe: VoiceProbeOutcome | null = null;
      let keySaveFailed = false;
      let keySaveDetail: string | null = null;
      if (apiKey.trim() !== "") {
        try {
          await putCredential.mutateAsync({
            runtimeId: runtime.id,
            credentialKey: VOICE_INSTANCE_CREDENTIAL_KEY,
            value: apiKey.trim(),
          });
          // RUYI-626: the verdict that matters is the device's (the direct
          // path dials Google from the user's network); it feeds the same
          // credential_probe badge and decides the feedback alert.
          probe = await probeVoiceCredential(apiKey.trim());
          void api
            .reportCredentialProbe(runtime.id, {
              status: probe.status,
              http_status: probe.httpStatus,
            })
            .catch(() => {});
        } catch (err) {
          keySaveFailed = true;
          // RUYI-540: surface the server's readable message (e.g. the
          // fail-closed 503 naming MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY)
          // instead of leaving a bare status code as the only hint.
          keySaveDetail = credentialSaveFailureDetail(err);
        }
      }
      Alert.alert(
        keySaveFailed
          ? t("voice_instance_create.key_save_failed")
          : probe?.status === "invalid"
            ? t("voice_instance_create.created_probe_invalid")
            : probe?.status === "unreachable"
              ? t("voice_instance_create.created_unreachable")
              : t("voice_instance_create.created"),
        keySaveDetail ?? undefined,
      );
      router.replace({
        pathname: "/[workspace]/more/runtimes/[id]",
        params: { workspace: wsSlug, id: runtime.id },
      });
    } catch {
      Alert.alert(t("voice_instance_create.create_failed"));
    } finally {
      setSubmitting(false);
    }
  };

  const createDefaultProfile = () => {
    if (!wsId) return;
    createProfile.mutate(
      // Voice families are API-enforced command-less: an empty command_name
      // is exactly what the server requires for gemini_live profiles.
      { display_name: "Gemini Live", protocol_family: "gemini_live", command_name: "" },
      {
        onSuccess: (profile: RuntimeProfile) => {
          setProfileId(profile.id);
          qc.invalidateQueries({ queryKey: runtimeProfileKeys.list(wsId) });
        },
        onError: () => {
          Alert.alert(t("voice_instance_create.create_failed"));
        },
      },
    );
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* modal 自绘头部（more/agents/new 同款）；顶部让出系统状态栏
          （RUYI-540）：modal 页没有 Stack 头部的安全区处理，Android
          edge-to-edge 下 pt-3 会被状态栏图标压住。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={tc("cancel", "Cancel")}
        >
          <Text className="text-base text-muted-foreground">
            {tc("cancel", "Cancel")}
          </Text>
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground" numberOfLines={1}>
          {t("voice_instance_create.title")}
        </Text>
        <Pressable
          onPress={submit}
          disabled={!canSubmit}
          className={`px-2 py-1 ${canSubmit ? "" : "opacity-40"}`}
          accessibilityRole="button"
          accessibilityLabel={t("voice_instance_create.submit")}
        >
          <Text className="text-base text-brand font-semibold">
            {submitting
              ? t("voice_instance_create.creating")
              : t("voice_instance_create.submit")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        <Field label={t("voice_instance.name_label")}>
          <TextInput
            value={name}
            onChangeText={setName}
            placeholder={t("voice_instance_create.name_placeholder")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            maxLength={200}
            autoFocus
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!submitting}
          />
        </Field>

        <Field label={t("voice_instance.type_label")}>
          {voiceProfiles.length === 0 ? (
            <View className="rounded-md border border-dashed border-border px-3 py-3 gap-2">
              <Text className="text-sm text-muted-foreground">
                {t("voice_instance_create.no_profiles")}
              </Text>
              <Pressable
                onPress={createDefaultProfile}
                disabled={createProfile.isPending}
                className={`self-start rounded-md border border-border px-3 py-1.5 active:bg-secondary ${
                  createProfile.isPending ? "opacity-40" : ""
                }`}
                accessibilityRole="button"
                accessibilityLabel={t("voice_instance_create.create_default_profile")}
              >
                <Text className="text-sm text-foreground">
                  {t("voice_instance_create.create_default_profile")}
                </Text>
              </Pressable>
            </View>
          ) : (
            <View className="rounded-md border border-border overflow-hidden">
              {voiceProfiles.map((profile, i) => {
                const selected = profile.id === effectiveProfileId;
                return (
                  <Pressable
                    key={profile.id}
                    onPress={() => setProfileId(profile.id)}
                    className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                      i > 0 ? "border-t border-border" : ""
                    }`}
                    accessibilityRole="radio"
                    accessibilityState={{ selected }}
                  >
                    <View
                      className={`size-4 rounded-full border items-center justify-center ${
                        selected ? "border-brand" : "border-muted-foreground"
                      }`}
                    >
                      {selected ? (
                        <View className="size-2 rounded-full bg-brand" />
                      ) : null}
                    </View>
                    <Text className="flex-1 text-sm text-foreground" numberOfLines={1}>
                      {profile.display_name}
                    </Text>
                    <Text className="text-xs text-muted-foreground">
                      {profile.protocol_family}
                    </Text>
                  </Pressable>
                );
              })}
            </View>
          )}
        </Field>

        <Field label={t("voice_instance.key_label")}>
          <TextInput
            value={apiKey}
            onChangeText={setApiKey}
            placeholder={t("voice_instance_create.key_placeholder")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            secureTextEntry
            autoCapitalize="none"
            autoCorrect={false}
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!submitting}
          />
          <Text className="text-xs text-muted-foreground">
            {t("voice_instance.key_hint")}
          </Text>
        </Field>

        <Field label={t("voice_instance.model_label")}>
          <TextInput
            value={model}
            onChangeText={setModel}
            placeholder={t("voice_instance.model_placeholder")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            maxLength={200}
            autoCapitalize="none"
            autoCorrect={false}
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!submitting}
          />
        </Field>

        <View className="gap-1.5">
          <Pressable
            onPress={() => setAdvancedOpen((v) => !v)}
            className="flex-row items-center gap-1"
            accessibilityRole="button"
            accessibilityLabel={t("voice_instance.advanced_label")}
          >
            <Text className="text-xs uppercase tracking-wider text-muted-foreground">
              {t("voice_instance.advanced_label")}
            </Text>
            <Text className="text-xs text-muted-foreground">
              {advancedOpen ? "▾" : "▸"}
            </Text>
          </Pressable>
          {advancedOpen ? (
            <View className="gap-1.5">
              <TextInput
                value={advancedText}
                onChangeText={setAdvancedText}
                placeholder={t("voice_instance.advanced_placeholder")}
                placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
                multiline
                textAlignVertical="top"
                autoCapitalize="none"
                autoCorrect={false}
                className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-16 font-mono"
                editable={!submitting}
              />
              {!advanced.ok ? (
                <Text className="text-xs text-destructive">
                  {t("voice_instance.advanced_invalid")}
                </Text>
              ) : null}
            </View>
          ) : null}
        </View>

        {/* Registration + visibility are server-fixed for voice instances
            (§4.3) — read-only, desktop parity. */}
        <View className="gap-1">
          <Text className="text-sm text-muted-foreground">
            {t("voice_instance.registration_label")}:{" "}
            {t("voice_instance.registration_manual")}
          </Text>
          <Text className="text-sm text-muted-foreground">
            {t("voice_instance.visibility_label")}:{" "}
            {t("voice_instance.visibility_workspace")}
          </Text>
        </View>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <View className="gap-1.5">
      <Text className="text-xs uppercase tracking-wider text-muted-foreground">
        {label}
      </Text>
      {children}
    </View>
  );
}
