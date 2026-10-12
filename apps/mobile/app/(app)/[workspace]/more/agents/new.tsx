/**
 * Create agent (`more/agents/new`, RUYI-346 A1) — modal create form, mobile
 * subset of web's create-agent dialog: name / description / instructions /
 * runtime / voice / visibility. Access granularity beyond private vs
 * whole-workspace (specific people) and conversation starters stay web-only
 * for P0.
 *
 * RUYI-425 §4.2: the runtime field is the text slot; the optional voice slot
 * picks a realtime-voice-capable instance (first fixed entry 不配置语音 =
 * unbound). Slots exclude each other's selection and filter by advertised
 * capabilities (`agentSlotChoices`); the voice rows carry the credential
 * tri-state badge. Agents never hold credential inputs — keys live on the
 * instance (§4.3/§4.5), not the agent.
 *
 * Visibility submits the authoritative MUL-3963 shape: `permission_mode:
 * "public_to"` + `invocation_targets: [{ target_type: "workspace" }]` for
 * workspace-wide, `permission_mode: "private"` + `[]` for private — never
 * the legacy `visibility` field.
 *
 * Runtime choices are the online runtimes usable by the current user
 * (`isRuntimeUsableForUser`, web parity). Create stays disabled until a
 * runtime is picked — `runtimeReady` gates on the list having loaded so the
 * button can't flash enabled before choices exist.
 */
import { useMemo, useState } from "react";
import { Alert, Pressable, ScrollView, TextInput, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { router } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useQuery } from "@tanstack/react-query";
import {
  agentSlotChoices,
  runtimeCredentialStatus,
} from "@multica/core/runtimes";
import type { CreateAgentRequest } from "@multica/core/types";
import { Text } from "@/components/ui/text";
import { MOBILE_PLACEHOLDER_COLOR } from "@/components/ui/input-tokens";
import { runtimeListOptions } from "@/data/queries/runtimes";
import { useCreateAgent } from "@/data/mutations/agents";
import { useAuthStore } from "@/data/auth-store";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

export default function NewAgentScreen() {
  const insets = useSafeAreaInsets();
  const me = useAuthStore((s) => s.user);
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { t } = useT("agents");
  const create = useCreateAgent();

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");
  const [runtimeId, setRuntimeId] = useState("");
  const [voiceRuntimeId, setVoiceRuntimeId] = useState("");
  const [workspaceVisible, setWorkspaceVisible] = useState(true);

  const { data: runtimes, isLoading: runtimesLoading } = useQuery(
    runtimeListOptions(wsId),
  );

  // Text slot: online + usable + text-capable, minus the voice slot's pick.
  const runtimeChoices = useMemo(
    () =>
      agentSlotChoices(runtimes, "text", {
        currentUserId: me?.id ?? null,
        excludeRuntimeId: voiceRuntimeId,
      }),
    [runtimes, me, voiceRuntimeId],
  );

  // Voice slot: realtime-voice-capable, minus the text slot's pick.
  const voiceChoices = useMemo(
    () =>
      agentSlotChoices(runtimes, "realtime_voice", {
        currentUserId: me?.id ?? null,
        excludeRuntimeId: runtimeId,
      }),
    [runtimes, me, runtimeId],
  );

  const runtimeReady = !runtimesLoading && runtimeChoices.length > 0;
  const canCreate =
    name.trim().length > 0 && runtimeId !== "" && runtimeReady && !create.isPending;

  const onCreate = () => {
    if (!canCreate) return;
    const body: CreateAgentRequest = {
      name: name.trim(),
      description,
      instructions,
      runtime_id: runtimeId,
      voice_runtime_id: voiceRuntimeId || undefined,
      permission_mode: workspaceVisible ? "public_to" : "private",
      invocation_targets: workspaceVisible
        ? [{ target_type: "workspace" }]
        : [],
    };
    create.mutate(body, {
      onSuccess: () => router.back(),
      onError: (err) => {
        Alert.alert(
          t("create_dialog.create_failed_toast", "Failed to create agent"),
          err instanceof Error ? err.message : undefined,
        );
      },
    });
  };

  return (
    <KeyboardAvoidingView className="flex-1 bg-background" behavior="padding">
      {/* modal 自绘头部；顶部让出系统状态栏（RUYI-540），见 more/runtimes/new。 */}
      <View
        className="flex-row items-center px-4 pb-2 border-b border-border"
        style={{ paddingTop: insets.top + 12 }}
      >
        <Pressable
          onPress={() => router.back()}
          hitSlop={8}
          accessibilityRole="button"
          accessibilityLabel={t("create_dialog.cancel", "Cancel")}
        >
          <Text className="text-base text-muted-foreground">
            {t("create_dialog.cancel", "Cancel")}
          </Text>
        </Pressable>
        <Text className="flex-1 text-center text-lg font-semibold text-foreground">
          {t("create_dialog.title_create", "Create Agent")}
        </Text>
        <Pressable
          onPress={onCreate}
          disabled={!canCreate}
          className={`px-2 py-1 ${canCreate ? "" : "opacity-40"}`}
          accessibilityRole="button"
          accessibilityLabel={t("create_dialog.create", "Create")}
        >
          <Text className="text-base text-brand font-semibold">
            {create.isPending
              ? t("create_dialog.creating", "Creating...")
              : t("create_dialog.create", "Create")}
          </Text>
        </Pressable>
      </View>

      <ScrollView
        className="flex-1"
        contentContainerClassName="px-4 pt-4 pb-8 gap-4"
        keyboardShouldPersistTaps="handled"
      >
        <Field label={t("create_dialog.name_label", "Name")}>
          <TextInput
            value={name}
            onChangeText={setName}
            placeholder={t("create_dialog.name_placeholder", "e.g. Deep Research Agent")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            autoFocus
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2"
            editable={!create.isPending}
          />
        </Field>

        <Field label={t("create_dialog.description_label", "Description")}>
          <TextInput
            value={description}
            onChangeText={setDescription}
            placeholder={t("create_dialog.description_placeholder", "What does this agent do?")}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            multiline
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-16"
            textAlignVertical="top"
            editable={!create.isPending}
          />
        </Field>

        <Field label={t("create_dialog.instructions.label", "Instructions")}>
          <TextInput
            value={instructions}
            onChangeText={setInstructions}
            placeholder={t(
              "create_dialog.instructions.editor_placeholder",
              "Write what this agent should do, what to focus on, what to avoid…",
            )}
            placeholderTextColor={MOBILE_PLACEHOLDER_COLOR}
            multiline
            className="text-base text-foreground bg-secondary/50 rounded-md px-3 py-2 min-h-32"
            textAlignVertical="top"
            editable={!create.isPending}
          />
        </Field>

        <Field label={t("create_dialog.runtime_label", "Runtime")}>
          {runtimesLoading ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("create_dialog.runtime_loading", "Loading runtimes...")}
            </Text>
          ) : runtimeChoices.length === 0 ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("create_dialog.runtime_none", "No runtime available")}
            </Text>
          ) : (
            <View className="rounded-md border border-border overflow-hidden">
              {runtimeChoices.map((r, i) => {
                const selected = r.id === runtimeId;
                return (
                  <Pressable
                    key={r.id}
                    onPress={() => setRuntimeId(r.id)}
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
                      {r.custom_name || r.name}
                    </Text>
                    {r.runtime_mode === "cloud" ? (
                      <Text className="text-xs text-muted-foreground">
                        {t("create_dialog.runtime_cloud_badge", "Cloud")}
                      </Text>
                    ) : null}
                  </Pressable>
                );
              })}
            </View>
          )}
        </Field>

        <Field label={t("create_dialog.voice_label", "Voice")}>
          {runtimesLoading ? (
            <Text className="text-sm text-muted-foreground py-1">
              {t("create_dialog.runtime_loading", "Loading runtimes...")}
            </Text>
          ) : (
            <View className="rounded-md border border-border overflow-hidden">
              {/* 首项固定：不配置语音（清空语音槽位） */}
              <Pressable
                onPress={() => setVoiceRuntimeId("")}
                className={`flex-row items-center gap-3 px-3 py-2.5 active:bg-secondary ${
                  voiceChoices.length > 0 ? "border-b border-border" : ""
                }`}
                accessibilityRole="radio"
                accessibilityState={{ selected: voiceRuntimeId === "" }}
              >
                <View
                  className={`size-4 rounded-full border items-center justify-center ${
                    voiceRuntimeId === "" ? "border-brand" : "border-muted-foreground"
                  }`}
                >
                  {voiceRuntimeId === "" ? (
                    <View className="size-2 rounded-full bg-brand" />
                  ) : null}
                </View>
                <Text className="flex-1 text-sm text-foreground">
                  {t("create_dialog.voice_none", "No voice")}
                </Text>
              </Pressable>
              {voiceChoices.length === 0 ? (
                <Text className="text-sm text-muted-foreground px-3 py-2.5">
                  {t("create_dialog.voice_empty", "No voice-capable instance")}
                </Text>
              ) : (
                voiceChoices.map((r, i) => {
                  const selected = r.id === voiceRuntimeId;
                  const badge = runtimeCredentialStatus(r);
                  return (
                    <Pressable
                      key={r.id}
                      onPress={() => setVoiceRuntimeId(r.id)}
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
                        {r.custom_name || r.name}
                      </Text>
                      <CredentialBadge status={badge} />
                    </Pressable>
                  );
                })
              )}
            </View>
          )}
        </Field>

        <Field label={t("create_dialog.visibility_label", "Visibility")}>
          <View className="gap-2">
            <VisibilityOption
              selected={workspaceVisible}
              onSelect={() => setWorkspaceVisible(true)}
              title={t("access.workspace_title", "Entire workspace")}
              description={t(
                "access.workspace_desc",
                "All workspace members can run this agent",
              )}
            />
            <VisibilityOption
              selected={!workspaceVisible}
              onSelect={() => setWorkspaceVisible(false)}
              title={t("access.private_title", "Only me")}
              description={t("access.private_desc", "Only you can run this agent")}
            />
          </View>
        </Field>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

// RUYI-425 §4.2 voice credential badge states. Text-only by design: the
// agent form never edits credentials, it only reflects the instance's state.
function CredentialBadge({
  status,
}: {
  status: "not_configured" | "configured" | "invalid" | "unreachable";
}) {
  const { t } = useT("agents");
  if (status === "configured") {
    return (
      <Text className="text-xs text-success">
        {t("create_dialog.credential_configured", "Configured ✓")}
      </Text>
    );
  }
  if (status === "invalid") {
    return (
      <Text className="text-xs text-destructive">
        {t("create_dialog.credential_invalid", "Key invalid")}
      </Text>
    );
  }
  if (status === "unreachable") {
    return (
      <Text className="text-xs text-warning">
        {t("create_dialog.credential_unreachable", "Can't verify")}
      </Text>
    );
  }
  return (
    <Text className="text-xs text-muted-foreground">
      {t("create_dialog.credential_not_configured", "Not configured")}
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

function VisibilityOption({
  selected,
  onSelect,
  title,
  description,
}: {
  selected: boolean;
  onSelect: () => void;
  title: string;
  description: string;
}) {
  const { colorScheme } = useColorScheme();
  return (
    <Pressable
      onPress={onSelect}
      className={`flex-row items-start gap-3 rounded-md border px-3 py-2.5 active:bg-secondary ${
        selected ? "border-brand bg-brand/5" : "border-border"
      }`}
      accessibilityRole="radio"
      accessibilityState={{ selected }}
      accessibilityLabel={title}
    >
      <View
        className={`mt-0.5 size-4 rounded-full border items-center justify-center ${
          selected ? "border-brand" : "border-muted-foreground"
        }`}
      >
        {selected ? <View className="size-2 rounded-full bg-brand" /> : null}
      </View>
      <View className="flex-1 gap-0.5">
        <Text className="text-sm font-medium text-foreground">{title}</Text>
        <Text className="text-xs text-muted-foreground">{description}</Text>
      </View>
      <Ionicons
        name={selected ? "globe-outline" : "lock-closed-outline"}
        size={16}
        color={THEME[colorScheme].mutedForeground}
      />
    </Pressable>
  );
}
