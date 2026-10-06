/**
 * Voice instance settings (`more/runtimes/[id]`, RUYI-425 §4.3 mobile) — RN
 * mirror of the desktop VoiceInstanceSettingsCard. The instance form is an
 * EDIT surface: manual creation is `more/runtimes/new`, so this page ships
 * on existing instances and presents the server-fixed §4.3 facts
 * (registration manual, status online from birth, visibility public/
 * workspace) read-only.
 *
 * Desktop parity, field by field:
 * - Name (duplicates allowed — display override, not an identity).
 * - Type read-only (protocol_family || provider).
 * - API key: password input that never echoes the stored value; update +
 *   clear; every save runs the server connectivity probe, surfacing the
 *   tri-state (saved / saved-but-probe-invalid / failed) — the probe never
 *   blocks the write (§4.5).
 * - Model (default placeholder gemini-3.8-live).
 * - Advanced params: JSON-object text; bad JSON shows the invalid error and
 *   the save is refused client-side (desktop parity).
 * - Enabled toggle: persists via metadata.disabled — agent_runtime.status
 *   only admits online/offline, so the gate lands in metadata (desktop
 *   parity). Disabled instances stay configured but are excluded from new
 *   agent bindings — the session gate reads this flag.
 *
 * Drafts re-seed from the server values (deps are the server fields, never
 * the local drafts) so background refetches don't clobber in-flight typing.
 */
import { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  Switch,
  TextInput,
  View,
} from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { Stack, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import { runtimeCredentialStatus, runtimeDisplayName } from "@multica/core/runtimes";
import {
  VOICE_INSTANCE_CREDENTIAL_KEY,
  parseAdvancedParams,
  readVoiceInstanceSettings,
} from "@/lib/voice-runtime";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { runtimeListOptions } from "@/data/queries/runtimes";
import {
  useDeleteRuntimeCredential,
  usePutRuntimeCredential,
  useUpdateRuntime,
} from "@/data/mutations/runtimes";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useT } from "@/lib/use-t";

export default function VoiceRuntimeSettingsScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const runtimeId = typeof id === "string" ? id : "";
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { t } = useT("runtimes");

  const updateRuntime = useUpdateRuntime(wsId);
  const putCredential = usePutRuntimeCredential(wsId);
  const deleteCredential = useDeleteRuntimeCredential(wsId);

  const { data: runtimes, isLoading } = useQuery(runtimeListOptions(wsId));
  const runtime = useMemo(
    () => runtimes?.find((r) => r.id === runtimeId),
    [runtimes, runtimeId],
  );

  const settings = readVoiceInstanceSettings(runtime?.metadata);

  const [name, setName] = useState("");
  const [model, setModel] = useState("");
  const [advancedText, setAdvancedText] = useState("");
  const [keyValue, setKeyValue] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);

  // Seeded once the instance resolves; re-seeded when the invalidated
  // queries deliver fresh server values. Deps are the server values —
  // background refetches never clobber in-flight typing (desktop parity).
  const advancedJson = useMemo(() => {
    const advanced = readVoiceInstanceSettings(runtime?.metadata).advanced;
    return advanced ? JSON.stringify(advanced, null, 2) : "";
  }, [runtime?.metadata]);
  useEffect(() => {
    setName(runtime?.custom_name ?? "");
  }, [runtime?.id, runtime?.custom_name]);
  useEffect(() => {
    setModel(settings.model);
  }, [runtime?.id, settings.model]);
  useEffect(() => {
    setAdvancedText(advancedJson);
  }, [runtime?.id, advancedJson]);

  const showError = (err: unknown, fallback: string) =>
    Alert.alert(err instanceof Error && err.message ? err.message : fallback);

  if (isLoading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator />
      </View>
    );
  }

  if (!runtime) {
    return (
      <View className="flex-1 items-center justify-center bg-background px-8">
        <Text className="text-sm text-muted-foreground text-center">
          {t("mobile.detail.not_found", "This instance no longer exists.")}
        </Text>
      </View>
    );
  }

  const credential = runtimeCredentialStatus(runtime);
  const displayName = runtimeDisplayName(runtime);
  const pending =
    updateRuntime.isPending || putCredential.isPending || deleteCredential.isPending;

  const saveName = () => {
    const next = name.trim();
    if (next === (runtime.custom_name ?? "")) return;
    updateRuntime.mutate(
      { runtimeId: runtime.id, patch: { custom_name: next } },
      {
        onSuccess: () => Alert.alert(t("voice_instance.name_saved")),
        onError: (err) => showError(err, t("voice_instance.save_failed")),
      },
    );
  };

  const saveModel = () => {
    if (model === settings.model) return;
    updateRuntime.mutate(
      { runtimeId: runtime.id, patch: { model } },
      {
        onSuccess: () => Alert.alert(t("voice_instance.model_saved")),
        onError: (err) => showError(err, t("voice_instance.save_failed")),
      },
    );
  };

  const saveAdvanced = () => {
    const parsed = parseAdvancedParams(advancedText);
    if (!parsed.ok) {
      Alert.alert(t("voice_instance.advanced_invalid"));
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
        onSuccess: () => Alert.alert(t("voice_instance.advanced_saved")),
        onError: (err) => showError(err, t("voice_instance.save_failed")),
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
            // only downgrades the alert and flips the badge.
            Alert.alert(
              t("voice_instance.probe_invalid", {
                status: res.probe?.http_status ?? "",
              }),
            );
          } else {
            Alert.alert(t("voice_instance.key_saved"));
          }
        },
        onError: (err) =>
          showError(err, t("voice_instance.key_update_failed")),
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
        onSuccess: () => Alert.alert(t("voice_instance.key_cleared")),
        onError: (err) =>
          showError(err, t("voice_instance.key_update_failed")),
      },
    );
  };

  const toggleEnabled = (enabled: boolean) => {
    updateRuntime.mutate(
      { runtimeId: runtime.id, patch: { disabled: !enabled } },
      {
        onError: (err) => showError(err, t("voice_instance.save_failed")),
      },
    );
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      <Stack.Screen options={{ title: displayName }} />
      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-5"
        keyboardShouldPersistTaps="handled"
      >
        {/* Header row: fixed facts + credential tri-state badge (desktop
            card header parity). */}
        <View className="gap-1">
          <Text className="text-lg font-semibold text-foreground" numberOfLines={1}>
            {displayName}
          </Text>
          <CredentialBadge status={credential} />
        </View>

        {/* 名称 — duplicates allowed. */}
        <Field label={t("voice_instance.name_label")}>
          <View className="flex-row gap-2">
            <TextInput
              value={name}
              onChangeText={setName}
              className="flex-1 text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
              editable={!pending}
            />
            <SaveButton
              label={t("voice_instance.save")}
              disabled={
                pending ||
                !name.trim() ||
                name.trim() === (runtime.custom_name ?? "")
              }
              onPress={saveName}
            />
          </View>
        </Field>

        {/* Type — read-only in edit mode (§4.3). */}
        <Field label={t("voice_instance.type_label")}>
          <ReadonlyValue value={runtime.protocol_family || runtime.provider} mono />
        </Field>

        {/* API Key — password control, never echoes the stored value. */}
        <Field label={t("voice_instance.key_label")}>
          <View className="flex-row gap-2">
            <TextInput
              value={keyValue}
              onChangeText={setKeyValue}
              placeholder={t("voice_instance.key_placeholder")}
              placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
              secureTextEntry
              autoCapitalize="none"
              autoCorrect={false}
              className="flex-1 text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
              editable={!pending}
            />
            <SaveButton
              label={t("voice_instance.key_update")}
              disabled={pending || !keyValue.trim()}
              onPress={updateKey}
            />
          </View>
          {credential !== "not_configured" ? (
            <Pressable
              onPress={clearKey}
              disabled={pending}
              className="self-start"
              hitSlop={8}
              accessibilityRole="button"
              accessibilityLabel={t("voice_instance.key_clear")}
            >
              <Text className="text-sm text-destructive">
                {t("voice_instance.key_clear")}
              </Text>
            </Pressable>
          ) : null}
          <Text className="text-xs text-muted-foreground">
            {t("voice_instance.key_hint")}
          </Text>
        </Field>

        {/* 模型. */}
        <Field label={t("voice_instance.model_label")}>
          <View className="flex-row gap-2">
            <TextInput
              value={model}
              onChangeText={setModel}
              placeholder={t("voice_instance.model_placeholder")}
              placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
              autoCapitalize="none"
              autoCorrect={false}
              className="flex-1 text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
              editable={!pending}
            />
            <SaveButton
              label={t("voice_instance.save")}
              disabled={pending || model === settings.model}
              onPress={saveModel}
            />
          </View>
        </Field>

        {/* 高级参数 — collapsed JSON block (§4.3). */}
        <Field label={t("voice_instance.advanced_label")}>
          <Pressable
            onPress={() => setAdvancedOpen((v) => !v)}
            className="self-start"
            hitSlop={8}
            accessibilityRole="button"
            accessibilityLabel={t("voice_instance.advanced_label")}
            accessibilityState={{ expanded: advancedOpen }}
          >
            <Text className="text-sm text-foreground underline">
              {advancedOpen ? "▾" : "▸"}{" "}
              {advancedOpen
                ? t("mobile.advanced.collapse", "Hide")
                : t("mobile.advanced.expand", "Show")}
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
                className="text-sm text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-20 font-mono"
                editable={!pending}
              />
              {!parseAdvancedParams(advancedText).ok ? (
                <Text className="text-xs text-destructive">
                  {t("voice_instance.advanced_invalid")}
                </Text>
              ) : null}
              <SaveButton
                label={t("voice_instance.save")}
                disabled={pending}
                onPress={saveAdvanced}
              />
            </View>
          ) : null}
        </Field>

        {/* 注册方式 — read-only (§4.3). */}
        <Field label={t("voice_instance.registration_label")}>
          <ReadonlyValue
            value={
              runtime.registration_source === "manual"
                ? t("voice_instance.registration_manual")
                : t("voice_instance.registration_daemon")
            }
          />
        </Field>

        {/* 启用 — metadata.disabled（会话闸门），status 不动。 */}
        <View className="flex-row items-center justify-between gap-3 border-t border-border pt-3">
          <View className="flex-1 min-w-0 gap-0.5">
            <Text className="text-sm font-medium text-foreground">
              {t("voice_instance.enabled_label")}
            </Text>
            <Text className="text-xs text-muted-foreground">
              {t("voice_instance.enabled_desc")}
            </Text>
          </View>
          <Switch
            value={!settings.disabled}
            disabled={pending}
            onValueChange={toggleEnabled}
            accessibilityLabel={t("voice_instance.enabled_label")}
          />
        </View>

        {/* 可见性 — fixed to workspace/public for voice instances (§4.3). */}
        <Field label={t("voice_instance.visibility_label")}>
          <ReadonlyValue value={t("voice_instance.visibility_workspace")} />
        </Field>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function CredentialBadge({
  status,
}: {
  status: "not_configured" | "configured" | "invalid";
}) {
  const { t } = useT("runtimes");
  return (
    <Text
      className={
        status === "configured"
          ? "text-xs text-success"
          : status === "invalid"
            ? "text-xs text-destructive"
            : "text-xs text-muted-foreground"
      }
    >
      {status === "configured"
        ? t("voice_instance.badge_configured")
        : status === "invalid"
          ? t("voice_instance.badge_invalid")
          : t("voice_instance.badge_not_configured")}
    </Text>
  );
}

function SaveButton({
  label,
  disabled,
  onPress,
}: {
  label: string;
  disabled: boolean;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      className={`self-center rounded-md border border-border px-3 py-2 active:bg-secondary ${
        disabled ? "opacity-40" : ""
      }`}
      accessibilityRole="button"
      accessibilityLabel={label}
    >
      <Text className="text-sm text-foreground">{label}</Text>
    </Pressable>
  );
}

function ReadonlyValue({ value, mono }: { value: string; mono?: boolean }) {
  return (
    <Text
      className={`rounded-md border border-border bg-muted/30 px-2 py-1.5 text-sm text-foreground ${
        mono ? "font-mono" : ""
      }`}
      numberOfLines={1}
    >
      {value}
    </Text>
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
